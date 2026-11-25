package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fastBootRetry shrinks the boot retry so a case about retrying does not wait
// minutes, and returns the attempt log.
func fastBootRetry(t *testing.T, limit time.Duration) *bytes.Buffer {
	t.Helper()
	return bootRetryWith(t, bootRetryPolicy{limit: limit, base: time.Millisecond, ceiling: 5 * time.Millisecond})
}

func bootRetryWith(t *testing.T, p bootRetryPolicy) *bytes.Buffer {
	t.Helper()
	saved := bootRetry
	log := &bytes.Buffer{}
	p.out = log
	bootRetry = p
	t.Cleanup(func() { bootRetry = saved })
	return log
}

// scriptedPlane answers each handshake with the next status of script, then
// 200 with planeConfig once the script runs out.
func scriptedPlane(t *testing.T, script ...int) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n < len(script) {
			if script[n] >= 300 && script[n] < 400 {
				w.Header().Set("Location", "https://elsewhere.invalid/")
			}
			w.WriteHeader(script[n])
			_, _ = w.Write([]byte(`{"message":"scripted"}`))
			return
		}
		w.Header().Set(LicenseManagedHeader, "true")
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// fakePlane serves the handshake and the import route through one func each,
// called with the 1-based count of that route. It records when each request
// arrived, so a test can tell a request that started after the boot's limit.
type fakePlane struct {
	srv *httptest.Server

	mu       sync.Mutex
	arrivals []time.Time
	posts    int
	puts     int
	stored   string
}

type planeRoute func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request)

func newFakePlane(t *testing.T, post, put planeRoute) *fakePlane {
	t.Helper()
	p := &fakePlane{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.arrivals = append(p.arrivals, time.Now())
		var route planeRoute
		n := 0
		switch {
		case r.Method == http.MethodPost && r.URL.Path == controlPlaneHandshakePath:
			p.posts++
			n, route = p.posts, post
		case r.Method == http.MethodPut && r.URL.Path == controlPlaneConfigurationPath:
			p.puts++
			n, route = p.puts, put
		}
		p.mu.Unlock()
		if route == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		route(p, n, w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakePlane) counts() (posts, puts int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posts, p.puts
}

// serveStored is a current plane's handshake: 412 until a document is
// stored, then the document.
func serveStored(p *fakePlane, _ int, w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	stored := p.stored
	p.mu.Unlock()
	if stored == "" {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"message":"no configuration is assigned to this sidecar"}`))
		return
	}
	_, _ = w.Write([]byte(stored))
}

// storeImport is the import route: it stores the first document and
// answers 409 afterwards, as the gateway does.
func storeImport(p *fakePlane, _ int, w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r.Body)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stored != "" {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"already configured"}`))
		return
	}
	p.stored = buf.String()
	_, _ = w.Write([]byte(p.stored))
}

// stall holds the request until the client gives up on it. The body is read
// first: the server notices a client that went away only once it has.
func stall(_ *fakePlane, _ int, _ http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	<-r.Context().Done()
}

// answerCut sends status with a body shorter than its Content-Length, so the
// client reads the status and then fails reading the body.
func answerCut(w http.ResponseWriter, status int) {
	if status >= 300 && status < 400 {
		w.Header().Set("Location", "https://elsewhere.invalid/")
	}
	w.Header().Set("Content-Length", "1000")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"message":"cut`))
}

// attemptLines decodes the retry lines the boot logged.
func attemptLines(t *testing.T, log *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(log.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// ENG-1153: a plane that answers 500 twice and then 200 boots the sidecar.
// Before, the first 500 stopped the process, and a fleet restarted during an
// outage waited out the platform's restart backoff.
func TestBootRetriesAFailingPlaneUntilItAnswers(t *testing.T) {
	log := fastBootRetry(t, time.Minute)
	srv, calls := scriptedPlane(t, http.StatusInternalServerError, http.StatusInternalServerError)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Errorf("listeners = %d, want the plane's one", len(cfg.Listeners))
	}
	if n := calls(); n != 3 {
		t.Errorf("handshakes = %d, want 3", n)
	}
	lines := attemptLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("logged %d retry lines, want one per failed attempt (2):\n%s", len(lines), log)
	}
	for i, l := range lines {
		if l["level"] != "WARN" || l["attempt"] != float64(i+1) || l["url"] != srv.URL ||
			!strings.Contains(l["error"].(string), "answered 500") || l["retry_in"] == nil {
			t.Errorf("retry line %d = %v, want a WARN naming attempt %d, the URL, the 500 and the wait", i, l, i+1)
		}
	}
}

// A dropped connection is retried like a 5xx: the plane's pod restarting
// cuts the socket before any status line.
func TestBootRetriesADroppedConnection(t *testing.T) {
	fastBootRetry(t, time.Minute)
	var mu sync.Mutex
	drops := 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		drop := drops > 0
		drops--
		mu.Unlock()
		if drop {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	if _, _, err := SetupWith("", nil, nil); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
}

// A refusal is the plane's answer, and asking again gets the same one: 401,
// 422, a redirect, 429 and any other 4xx stop the boot after one request,
// with the message the operator has always read.
func TestBootStopsAtOnceOnARefusal(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "rejected the token"},
		{http.StatusUnprocessableEntity, "cannot build this sidecar's config: scripted"},
		{http.StatusMovedPermanently, "redirected to"},
		{http.StatusTemporaryRedirect, "redirected to"},
		{http.StatusForbidden, "answered 403 Forbidden: scripted"},
		// The gateway answers 429 only on the login routes; on the
		// handshake it is not an outage the boot waits out.
		{http.StatusTooManyRequests, "answered 429 Too Many Requests: scripted"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			log := fastBootRetry(t, time.Minute)
			srv, calls := scriptedPlane(t, tc.status)
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			t.Setenv(SidecarTokenEnv, "hsc_env")

			_, _, err := SetupWith("", nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one saying %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "gave up") || errors.As(err, new(planeUnavailableError)) {
				t.Errorf("err = %v, a refusal is not a retry that ran out", err)
			}
			if n := calls(); n != 1 {
				t.Errorf("handshakes = %d, want 1", n)
			}
			if log.Len() != 0 {
				t.Errorf("a refusal logged retries:\n%s", log)
			}
		})
	}
}

// The limit holds: a plane that never recovers stops the boot, as before the
// retry, with the plane's last answer and how long it tried.
func TestBootGivesUpAfterTheLimit(t *testing.T) {
	fastBootRetry(t, 50*time.Millisecond)
	srv, calls := scriptedPlane(t, slices.Repeat([]int{http.StatusServiceUnavailable}, 10_000)...)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	start := time.Now()
	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a plane that never answered booted the sidecar")
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("gave up after %s, want about the 50ms limit", took)
	}
	for _, want := range []string{"answered 503 Service Unavailable", "gave up after", "in 50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	}
	if !errors.As(err, new(planeUnavailableError)) {
		t.Errorf("err = %v, want it to still unwrap to planeUnavailableError", err)
	}
	if calls() < 2 {
		t.Errorf("handshakes = %d, want the boot to have retried", calls())
	}
}

// One deadline bounds the whole boot: no request starts after it, and a
// request in flight when it passes is cut there instead of running to its own
// 15 s timeout. Each case stops a different step of the boot.
func TestBootDeadlineBoundsEveryStep(t *testing.T) {
	const limit = 300 * time.Millisecond
	answer := func(status int) planeRoute {
		return func(_ *fakePlane, _ int, w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"scripted"}`))
		}
	}
	firstThen := func(first, rest planeRoute) planeRoute {
		return func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				first(p, n, w, r)
				return
			}
			rest(p, n, w, r)
		}
	}
	for _, tc := range []struct {
		name    string
		ceiling time.Duration
		post    planeRoute
		put     planeRoute
		puts    bool
	}{
		{name: "the handshake stalls", post: stall},
		// A wait drawn longer than what is left ends at the limit; it
		// never ends in one more request.
		{name: "the waits outlast the limit", ceiling: 10 * time.Second, post: answer(http.StatusServiceUnavailable)},
		// The 412 arrives just before the limit: the PUT, if it starts,
		// starts inside it, and nothing after it does.
		{name: "the 412 arrives at the limit", post: firstThen(func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			time.Sleep(limit - 40*time.Millisecond)
			serveStored(p, n, w, r)
		}, stall), put: storeImport},
		{name: "the import PUT stalls", post: serveStored, put: stall, puts: true},
		{name: "the handshake after the import stalls", post: firstThen(serveStored, stall), put: storeImport, puts: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ceiling := tc.ceiling
			if ceiling == 0 {
				ceiling = 5 * time.Millisecond
			}
			bootRetryWith(t, bootRetryPolicy{limit: limit, base: ceiling, ceiling: ceiling})
			plane := newFakePlane(t, tc.post, tc.put)
			t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
			t.Setenv(SidecarTokenEnv, "hsc_env")

			start := time.Now()
			_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
			took := time.Since(start)
			if err == nil {
				t.Fatal("the boot succeeded past its limit")
			}
			if !strings.Contains(err.Error(), "gave up") {
				t.Errorf("err = %v, want it to say the boot gave up", err)
			}
			if errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, the limit is not a stop", err)
			}
			if took > limit+700*time.Millisecond {
				t.Errorf("the boot took %s, want about the %s limit", took, limit)
			}
			plane.mu.Lock()
			arrivals := slices.Clone(plane.arrivals)
			plane.mu.Unlock()
			for i, at := range arrivals {
				if late := at.Sub(start) - limit; late > 20*time.Millisecond {
					t.Errorf("request %d arrived %s after the limit", i+1, late)
				}
			}
			if _, puts := plane.counts(); tc.puts && puts == 0 {
				t.Errorf("the import was never sent; the case does not test the step it names")
			}
		})
	}
}

// A body cut short does not change what a status means. A cut success or a
// cut 5xx is not a usable answer, so the boot asks again; a cut refusal is
// still a refusal and stops the boot after one request, and a cut 412 still
// imports. On both routes the boot uses.
func TestACutBodyKeepsTheMeaningOfItsStatus(t *testing.T) {
	cutOnce := func(status int, then planeRoute) planeRoute {
		return func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				if r.Method == http.MethodPut && status < 300 {
					storeImport(p, n, httptest.NewRecorder(), r)
				}
				answerCut(w, status)
				return
			}
			then(p, n, w, r)
		}
	}
	for _, tc := range []struct {
		route  string
		status int
		// want is what the error says; empty when the boot starts.
		want                string
		wantPosts, wantPuts int
	}{
		{"handshake", http.StatusOK, "", 3, 1},
		{"handshake", http.StatusServiceUnavailable, "", 3, 1},
		{"handshake", http.StatusUnauthorized, "rejected the token", 1, 0},
		{"handshake", http.StatusPreconditionFailed, "", 2, 1},
		{"handshake", http.StatusUnprocessableEntity, "cannot build this sidecar's config", 1, 0},
		{"handshake", http.StatusFound, "redirected to", 1, 0},
		{"handshake", http.StatusTooManyRequests, "answered 429", 1, 0},
		// The cut 200 stored the document; the retry gets 409 and serves
		// what the plane holds.
		{"import", http.StatusOK, "", 2, 2},
		{"import", http.StatusBadGateway, "", 2, 2},
		{"import", http.StatusUnauthorized, "rejected the token", 1, 1},
		{"import", http.StatusUnprocessableEntity, "refused the imported config", 1, 1},
		{"import", http.StatusFound, "redirected to", 1, 1},
		{"import", http.StatusTooManyRequests, "answered 429", 1, 1},
	} {
		t.Run(fmt.Sprintf("%s %d", tc.route, tc.status), func(t *testing.T) {
			log := fastBootRetry(t, time.Minute)
			post, put := planeRoute(serveStored), planeRoute(storeImport)
			if tc.route == "handshake" {
				post = cutOnce(tc.status, serveStored)
			} else {
				put = cutOnce(tc.status, storeImport)
			}
			plane := newFakePlane(t, post, put)
			t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
			t.Setenv(SidecarTokenEnv, "hsc_env")

			_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
			retried := len(attemptLines(t, log))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("SetupWith: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one saying %q", err, tc.want)
			case tc.want != "" && retried != 0:
				t.Errorf("a cut refusal was retried:\n%s", log)
			}
			if posts, puts := plane.counts(); posts != tc.wantPosts || puts != tc.wantPuts {
				t.Errorf("handshakes = %d, imports = %d; want %d and %d", posts, puts, tc.wantPosts, tc.wantPuts)
			}
		})
	}
}

// A caller's cancel stops the boot inside its retry, at once, and says it
// was a stop: an entry point that handles signals itself (the CLI waiting
// for a plane's config) must not wait out the limit after Ctrl-C.
func TestACancelStopsTheBootRetry(t *testing.T) {
	bootRetryWith(t, bootRetryPolicy{limit: time.Minute, base: 2 * time.Second, ceiling: 2 * time.Second})
	srv, _ := scriptedPlane(t, slices.Repeat([]int{http.StatusInternalServerError}, 10_000)...)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, _, err := SetupWith("", nil, nil, WithContext(ctx))
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "stopped while waiting") {
		t.Fatalf("err = %v, want a stop that unwraps to context.Canceled", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("the stop took %s, want it at once", took)
	}
}

// A check is not a boot: -validate, or the CLI's Connect page testing a URL a
// person typed, reports a plane that is down after one request.
func TestWithoutBootRetryFailsAtOnce(t *testing.T) {
	log := fastBootRetry(t, time.Minute)
	srv, calls := scriptedPlane(t, http.StatusInternalServerError)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	_, _, err := SetupWith("", nil, nil, WithoutBootRetry())
	if err == nil || !strings.Contains(err.Error(), "answered 500") {
		t.Fatalf("err = %v, want the 500", err)
	}
	if n := calls(); n != 1 || log.Len() != 0 {
		t.Errorf("handshakes = %d, log %q; want one request and no retry", n, log)
	}
}

// A transport error stays reachable under planeUnavailableError, so a caller
// can still ask the *url.Error what happened.
func TestAnUnreachablePlaneKeepsItsTransportError(t *testing.T) {
	fastBootRetry(t, time.Minute)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	t.Setenv(ControlPlaneURLEnv, "http://"+addr)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	_, _, err = SetupWith("", nil, nil, WithoutBootRetry())
	var ue *url.Error
	var pu planeUnavailableError
	if !errors.As(err, &pu) || pu.status != 0 || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want planeUnavailableError (no status) over a *url.Error", err)
	}
}

// A request that runs into the client's own timeout is an outage, like a
// refused connection.
func TestARequestTimeoutIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, derr := client.Get(srv.URL)
	if derr == nil {
		t.Fatal("the stalled server answered")
	}
	err := unreachable(srv.URL, derr)
	var ue *url.Error
	if !errors.As(err, new(planeUnavailableError)) || !errors.As(err, &ue) || !ue.Timeout() {
		t.Fatalf("unreachable(%v) = %v, want a retried timeout", derr, err)
	}
}

// A certificate the client refuses names a wrong URL or CA, not an outage.
// Retrying it would only delay the message by the whole limit.
func TestBootDoesNotRetryARefusedCertificate(t *testing.T) {
	log := fastBootRetry(t, time.Minute)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	_, _, err := SetupWith("", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "is unreachable") {
		t.Fatalf("err = %v, want the unreachable error", err)
	}
	if errors.As(err, new(planeUnavailableError)) || log.Len() != 0 {
		t.Errorf("a refused certificate was retried: err %v, log:\n%s", err, log)
	}
}

// https:// against a port that does not speak TLS fails the TLS record
// header. That is a wrong URL, not an outage. net/http names a port that
// answers plain HTTP http.ErrSchemeMismatch; any other protocol reaches the
// caller as tls.RecordHeaderError.
func TestBootDoesNotRetryATLSRecordHeaderFailure(t *testing.T) {
	plain, _ := scriptedPlane(t)
	ssh, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ssh.Close() })
	go func() {
		for {
			conn, err := ssh.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
			_ = conn.Close()
		}
	}()
	for _, tc := range []struct {
		name, url string
		is        func(error) bool
	}{
		{"plain HTTP", strings.Replace(plain.URL, "http://", "https://", 1),
			func(err error) bool { return errors.Is(err, http.ErrSchemeMismatch) }},
		{"another protocol", "https://" + ssh.Addr().String(),
			func(err error) bool { return errors.As(err, new(tls.RecordHeaderError)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fastBootRetry(t, time.Minute)
			t.Setenv(ControlPlaneURLEnv, tc.url)
			t.Setenv(SidecarTokenEnv, "hsc_env")

			_, _, err := SetupWith("", nil, nil)
			if err == nil || !tc.is(err) {
				t.Fatalf("err = %v, want the TLS failure", err)
			}
			if errors.As(err, new(planeUnavailableError)) || log.Len() != 0 {
				t.Errorf("a TLS record header failure was retried: err %v, log:\n%s", err, log)
			}
		})
	}
}

// 412 keeps the import path, and the import's own requests retry under the
// same rule: here the handshake fails once, the plane answers 412, the PUT
// fails once, and the handshake after the import fails once.
func TestBootRetriesTheImportPath(t *testing.T) {
	log := fastBootRetry(t, time.Minute)
	plane := newFakePlane(t,
		func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 || n == 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			serveStored(p, n, w, r)
		},
		func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			storeImport(p, n, w, r)
		})
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if !cfg.cp.imported {
		t.Error("the file was not imported")
	}
	if posts, puts := plane.counts(); posts != 4 || puts != 2 {
		t.Errorf("handshakes = %d, imports = %d; want 4 and 2", posts, puts)
	}
	var whats []string
	for _, l := range attemptLines(t, log) {
		whats = append(whats, l["msg"].(string))
	}
	want := []string{
		"the control plane is unavailable; retrying the boot handshake",
		"the control plane is unavailable; retrying the configuration import",
		"the control plane is unavailable; retrying the handshake after the configuration import",
	}
	if strings.Join(whats, "|") != strings.Join(want, "|") {
		t.Errorf("retry lines = %q, want %q", whats, want)
	}
}

// The heartbeat does not change: a 5xx there is one logged miss per tick,
// never a retry loop inside the tick.
func TestHeartbeatReimportDoesNotRetry(t *testing.T) {
	fastBootRetry(t, time.Minute)
	srv, calls := scriptedPlane(t, http.StatusInternalServerError, http.StatusInternalServerError)
	_, _, err := importLocalConfig(srv.URL, tokenCredential("hsc_x"), mustLoad(t, minimalConfig+`}`), nil)
	if err == nil || !strings.Contains(err.Error(), "answered 500") {
		t.Fatalf("err = %v, want the 500", err)
	}
	if n := calls(); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestFullJitterStaysInItsWindow(t *testing.T) {
	base, ceiling := 100*time.Millisecond, time.Second
	for n, window := range map[int]time.Duration{1: base, 2: 2 * base, 4: 8 * base, 5: ceiling, 60: ceiling} {
		for range 200 {
			if d := fullJitter(n, base, ceiling); d < 0 || d >= window {
				t.Fatalf("fullJitter(%d) = %s, want [0, %s)", n, d, window)
			}
		}
	}
}

// Full jitter spreads a fleet over the whole window. Thousands of sidecars
// fail their first handshake in the same second; waits that cluster (all
// zero, all the window, or a narrow band) send them back together.
func TestFullJitterSpreadsOverTheWindow(t *testing.T) {
	const samples = 4000
	base, ceiling := 100*time.Millisecond, time.Second
	window := 4 * base // attempt 3
	var buckets [10]int
	var sum time.Duration
	lo, hi := window, time.Duration(0)
	for range samples {
		d := fullJitter(3, base, ceiling)
		buckets[int(d*10/window)]++
		sum += d
		lo, hi = min(lo, d), max(hi, d)
	}
	for i, n := range buckets {
		// Uniform puts 400 in each tenth; 300 is over five standard
		// deviations below.
		if n < 300 {
			t.Errorf("tenth %d of the window got %d of %d waits, want about %d: %v", i, n, samples, samples/10, buckets)
		}
	}
	if mean := sum / samples; mean < window*45/100 || mean > window*55/100 {
		t.Errorf("mean wait = %s, want about half the %s window", mean, window)
	}
	if lo > window/50 || hi < window*49/50 {
		t.Errorf("waits span [%s, %s], want close to [0, %s)", lo, hi, window)
	}
}

func mustLoad(t *testing.T, body string) *Config {
	t.Helper()
	cfg, err := LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// ENG-1153 (Qodo): the import PUT is retried, and a write can land while its
// answer is lost (a 5xx after the commit, a timeout, a cut body). The retry
// then gets 409, as it does when someone else wrote in between. The served
// document cannot tell the two apart, so a 409 after a retry is uncertain:
// neither imported nor the file reported as ignored. A 409 on the first
// attempt stays someone else's write.
func TestARetriedImportThatEndsIn409IsUncertain(t *testing.T) {
	landThen := func(answer func(w http.ResponseWriter)) planeRoute {
		return func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				storeImport(p, n, httptest.NewRecorder(), r)
				answer(w)
				return
			}
			storeImport(p, n, w, r)
		}
	}
	otherAuthor := func(firstFails bool) planeRoute {
		return func(p *fakePlane, n int, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				p.mu.Lock()
				p.stored = planeConfig
				p.mu.Unlock()
				if firstFails {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
			storeImport(p, n, w, r)
		}
	}
	const (
		uncertain = iota
		lost
	)
	for _, tc := range []struct {
		name string
		put  planeRoute
		want int
	}{
		{"the first write landed and answered 503", landThen(func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }), uncertain},
		{"the first write landed and its body was cut", landThen(func(w http.ResponseWriter) { answerCut(w, http.StatusOK) }), uncertain},
		{"another author wrote while the first attempt failed", otherAuthor(true), uncertain},
		{"another author wrote before the first attempt", otherAuthor(false), lost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastBootRetry(t, time.Minute)
			plane := newFakePlane(t, serveStored, tc.put)
			t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
			t.Setenv(SidecarTokenEnv, "hsc_env")

			cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
			if err != nil {
				t.Fatalf("SetupWith: %v", err)
			}
			if cfg.cp.imported {
				t.Error("imported: a 409 never proves this process's write landed")
			}
			if got := cfg.cp.importUncertain; got != (tc.want == uncertain) {
				t.Errorf("importUncertain = %v, want %v", got, tc.want == uncertain)
			}
			if ignored := cfg.cp.fileListeners > 0; ignored != (tc.want == lost) {
				t.Errorf("file listeners reported ignored = %v, want %v", ignored, tc.want == lost)
			}
		})
	}
}

// ENG-1153 (Qodo): WithBootLog takes the boot retry's lines, so an entry
// point that converts the daemon's log format converts these too. Without
// it they go to stderr (here the policy's buffer).
func TestWithBootLogTakesTheRetryLines(t *testing.T) {
	policyOut := fastBootRetry(t, time.Minute)
	srv, _ := scriptedPlane(t, http.StatusInternalServerError)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	var boot bytes.Buffer
	if _, _, err := SetupWith("", nil, nil, WithBootLog(&boot)); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if lines := attemptLines(t, &boot); len(lines) != 1 || lines[0]["msg"] != "the control plane is unavailable; retrying the boot handshake" {
		t.Errorf("boot log = %q, want the one retry line", boot.String())
	}
	if policyOut.Len() != 0 {
		t.Errorf("the retry line also went to the default output: %q", policyOut.String())
	}
}
