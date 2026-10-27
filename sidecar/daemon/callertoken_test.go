package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
)

// caller_token admits a request by a token from a file. Each refusal below
// is a config that would otherwise admit everyone, name the caller twice,
// tear a header the upstream needs out of every request, or put the token
// in the audit trail.
func TestCallerTokenIsRefusedWhereItCannotWork(t *testing.T) {
	const ok = `"caller_token":{"file":"/etc/tokens","name":"agents"}`
	for _, tc := range []struct{ name, listener, want string }{
		{"off http", `"protocol":"postgres",` + ok,
			"caller_token is only supported on http"},
		{"grpc", `"protocol":"grpc",` + ok,
			"caller_token is only supported on http"},
		{"beside identity_header", `"protocol":"http","identity_header":"x-user",` + ok,
			"caller_token and identity_header both name the caller"},
		{"beside google_identity", `"protocol":"http","google_identity":{},` + ok,
			"caller_token and google_identity both name the caller"},
		{"no file", `"protocol":"http","caller_token":{"name":"agents"}`,
			"caller_token.file is required"},
		{"no name", `"protocol":"http","caller_token":{"file":"/etc/tokens","name":"  "}`,
			"caller_token.name is required"},
		{"not a header name", `"protocol":"http","caller_token":{"header":"x token","file":"/etc/tokens","name":"agents"}`,
			"is not a header name"},
		{"captured by http.headers", `"protocol":"http","http":{"headers":["X-Hoop-Token"]},` + ok,
			"http.headers lists \"x-hoop-token\", the caller_token header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{"listeners": [{"listen":":1","upstream":"h:1",`+tc.listener+`}]}`)
			if got := loadErr(t, p); !strings.Contains(got, tc.want) {
				t.Errorf("error %q does not say %q", got, tc.want)
			}
		})
	}

	// Stripped from every request, so never one the upstream, the framing
	// or the sidecar reads.
	for _, h := range []string{"Authorization", "cookie", "Content-Length", "transfer-encoding", "host", "via",
		"x-forwarded-host", analyzer.HeaderReviewMode, analyzer.HeaderApprovalMode} {
		t.Run("header "+h, func(t *testing.T) {
			p := writeConfig(t, `{"listeners": [{"protocol":"http","listen":":1","upstream":"h:1",`+
				`"caller_token":{"header":"`+h+`","file":"/etc/tokens","name":"agents"}}]}`)
			if got := loadErr(t, p); !strings.Contains(got, "pick another") {
				t.Errorf("error %q does not refuse header %q", got, h)
			}
		})
	}
}

// The file is the sidecar host's, so config validation never reads it: the
// plane validates without it, and a reload must not be refused over it.
func TestCallerTokenLoadsWithoutReadingTheFile(t *testing.T) {
	p := writeConfig(t, `{"listeners": [{"protocol":"http","listen":":1","upstream":"h:1",
	  "caller_token":{"file":"/nonexistent/tokens","name":"agents"}}]}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("caller_token refused on an http lane: %v", err)
	}
	lc := cfg.Listeners[0]
	if got := lc.credentialHeader(); got != defaultCallerTokenHeader {
		t.Errorf("credential header = %q, want %q", got, defaultCallerTokenHeader)
	}
	if !lc.authenticatesCaller() {
		t.Error("a caller_token lane does not authenticate its caller")
	}

	custom := ListenerConfig{Protocol: "http", CallerToken: &CallerTokenConfig{Header: " X-Agent-Key "}}
	if got := custom.credentialHeader(); got != "x-agent-key" {
		t.Errorf("credential header = %q, want the configured one lowercased", got)
	}
	// Only caller_token lanes strip and answer after identity.
	for _, other := range []ListenerConfig{
		{Protocol: "http", IdentityHeader: "x-user"},
		{Protocol: "http", GoogleIdentity: &GoogleIdentityConfig{}},
		{Protocol: "http"},
	} {
		if other.authenticatesCaller() {
			t.Errorf("%+v authenticates its caller", other)
		}
	}
}

// -validate and startup are where the file is read: a lane that cannot
// know its tokens must not serve, and the error names the file.
func TestValidateNamesAMissingTokenFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "tokens")
	p := writeConfig(t, `{"listeners": [{"name":"agents-api","protocol":"http","listen":"127.0.0.1:0","upstream":"h:1",
	  "caller_token":{"file":"`+missing+`","name":"agents"}}]}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Validate(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "caller_token.file") {
		t.Fatalf("Validate error = %v, want one naming caller_token.file and %s", err, missing)
	}
}

// A lane built without its token file does not start: serving it without a
// resolver would admit everyone.
func TestACallerTokenLaneWithoutItsFileDoesNotBuild(t *testing.T) {
	ln := lane{name: "api", cfg: ListenerConfig{Name: "api", Protocol: "http", Listen: "127.0.0.1:0",
		Upstream: "127.0.0.1:1", CallerToken: &CallerTokenConfig{File: "/x", Name: "agents"}}}
	ln.codecFactory = laneCodecFactory(ln.cfg, nil)
	if _, err := buildServer(ln, AuditConfig{}, nil, nil, nil, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("buildServer started a caller_token lane with no token file")
	}
}

const (
	goodToken = "good-caller-token-0001"
	newToken  = "rotated-caller-token-0002"
)

// tokenLane serves one caller_token lane, built as Run builds it, in front
// of upstream. It returns the lane's address, its audit trail, the lane and
// its token file's path.
func tokenLane(t *testing.T, upstream string, tokens string) (string, *sshTestSink, lane, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte(tokens), 0o600); err != nil {
		t.Fatal(err)
	}
	listen := "127.0.0.1:0"
	if strings.Contains(upstream, "://") {
		listen = "http://127.0.0.1:0"
	}
	// "api" is the lane pendingReviews files its review under.
	ln := lane{name: "api", cfg: ListenerConfig{Name: "api", Protocol: "http",
		Listen: listen, Upstream: upstream,
		CallerToken: &CallerTokenConfig{File: path, Name: "claude-agents"}}}
	ln.codecFactory = laneCodecFactory(ln.cfg, nil)
	lanes := []lane{ln}
	if _, err := openCallerTokens(lanes, nil); err != nil {
		t.Fatal(err)
	}
	sink := &sshTestSink{}
	srv, err := buildServer(lanes[0], AuditConfig{}, sink, pendingReviews(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	return waitForAddr(t, srv), sink, lanes[0], path
}

// recordingUpstream answers 200 and keeps the headers of every request it
// received.
type recordingUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	u := &recordingUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.headers = append(u.headers, r.Header.Clone())
		u.mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *recordingUpstream) received() []http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]http.Header(nil), u.headers...)
}

// rawRequest sends head as written on a fresh connection and reads the
// answer: net/http's client cannot send an empty or repeated header as is.
func rawRequest(t *testing.T, addr, head string) (int, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, head); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response to %q: %v", head, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func getWith(path string, tokenLines ...string) string {
	head := "GET " + path + " HTTP/1.1\r\nHost: api.internal\r\n"
	for _, l := range tokenLines {
		head += l + "\r\n"
	}
	return head + "Connection: close\r\n\r\n"
}

// The ticket's cases, end to end: only a listed token reaches the upstream,
// the upstream never sees the token, and the trail names the configured
// caller and never holds the token.
func TestACallerTokenLaneAdmitsOnlyAListedToken(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, sink, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), "#not-a-comment-but-a-token\n"+goodToken+"\n")

	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"no header", nil, "request refused: the request carried no credential"},
		{"empty", []string{"X-Hoop-Token:"}, "request refused: the request carried no credential"},
		{"whitespace only", []string{"X-Hoop-Token:  \t "}, "request refused: the request carried no credential"},
		{"wrong", []string{"X-Hoop-Token: wrong-token"}, "request refused: the caller token is not accepted"},
		{"repeated", []string{"X-Hoop-Token: " + goodToken, "X-Hoop-Token: " + goodToken},
			"request refused: the caller token is not accepted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := rawRequest(t, addr, getWith("/v1/items", tc.lines...))
			if code != http.StatusForbidden || !strings.Contains(body, tc.want) {
				t.Errorf("answer = %d %q, want 403 saying %q", code, body, tc.want)
			}
		})
	}
	if n := len(up.received()); n != 0 {
		t.Fatalf("the upstream received %d refused requests", n)
	}

	code, body := rawRequest(t, addr, getWith("/v1/items", "x-hoop-token:  "+goodToken+" "))
	if code != http.StatusOK || body != "ok" {
		t.Fatalf("a listed token got %d %q, want the upstream's 200", code, body)
	}
	got := up.received()
	if len(got) != 1 {
		t.Fatalf("the upstream received %d requests, want 1", len(got))
	}
	if v, ok := got[0]["X-Hoop-Token"]; ok {
		t.Errorf("the upstream received the token header: %q", v)
	}

	events := sink.snapshot()
	var admitted bool
	for _, ev := range events {
		if ev.Kind == audit.KindStatement && ev.Allowed && ev.Principal == "claude-agents" {
			admitted = true
		}
		if ev.Kind == audit.KindViolation && ev.Principal != "anonymous" {
			t.Errorf("a refused request is filed under %q, want anonymous", ev.Principal)
		}
	}
	if !admitted {
		t.Errorf("no allowed statement names the caller claude-agents: %+v", events)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), goodToken) {
		t.Error("the audit trail holds the token")
	}
}

// The gate judges a request once its body has arrived. A stranger's head is
// refused before that: whether its body never comes, comes chunked, or the
// request rides behind an admitted one.
func TestACallerTokenLaneRefusesAHeadBeforeItsBody(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, sink, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	for name, head := range map[string]string{
		"content-length, no body": "DELETE /v1/items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\n\r\n",
		"chunked, no chunk":       "POST /v1/items HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n",
		"behind an admitted request": "GET /v1/ok HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: " + goodToken + "\r\n\r\n" +
			"DELETE /v1/items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			code, body := rawRequest(t, addr, head)
			if code != http.StatusForbidden || !strings.Contains(body, "request refused: the request carried no credential") {
				t.Errorf("answer = %d %q, want the identity 403", code, body)
			}
		})
	}
	// Long enough for a forwarded head to have reached the handler.
	time.Sleep(100 * time.Millisecond)
	if n := len(up.received()); n != 0 {
		t.Fatalf("the upstream received %d requests: %v", n, up.received())
	}
	var refused int
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindViolation && ev.Rule == "identity" && ev.Statement == "DELETE /v1/items/*" {
			refused++
		}
	}
	if refused != 2 {
		t.Errorf("%d identity violations name DELETE /v1/items/*, want 2", refused)
	}
}

// HTTP/2 headers arrive before any DATA frame, and the bridge writes the
// head into the relay before the body: the same refusal applies.
func TestACallerTokenLaneRefusesHTTP2HeadersBeforeTheirBody(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, _, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p}
	t.Cleanup(tr.CloseIdleConnections)
	body, w := io.Pipe()
	t.Cleanup(func() { _ = w.Close() })
	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/v1/items/1", body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 10 // the body never comes
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("h2 request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(up.received()); n != 0 {
		t.Fatalf("the upstream received %d requests: %v", n, up.received())
	}
}

// A token file rotates without a restart: the new token runs beside the old
// one, then the old one goes.
func TestACallerTokenLaneRotatesItsTokens(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, _, ln, path := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	rewrite := func(content string, age time.Duration) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		mod := time.Now().Add(-age)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
		ln.callerTokens.Poll() // one tick of watchCallerTokens
	}
	status := func(token string) int {
		code, _ := rawRequest(t, addr, getWith("/v1/items", "X-Hoop-Token: "+token))
		return code
	}

	rewrite(goodToken+"\n"+newToken+"\n", 2*time.Hour)
	if status(goodToken) != 200 || status(newToken) != 200 {
		t.Fatal("during the overlap both tokens must be admitted")
	}
	rewrite(newToken+"\n", time.Hour)
	if status(goodToken) != 403 || status(newToken) != 200 {
		t.Fatal("after rotation only the new token must be admitted")
	}
	// A broken file keeps the token in force.
	rewrite("", 30*time.Minute)
	if status(newToken) != 200 {
		t.Fatal("an emptied file dropped the token in force")
	}
}

// The reserved route is as public as the listener, so on a caller_token lane
// it answers only a caller the token admits, and never reads the plane for
// one it refuses.
func TestACallerTokenLaneGuardsItsReservedRoute(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, _, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	path := ReviewStatusPath + laneReviewID

	if code, body := rawRequest(t, addr, getWith(path)); code != http.StatusForbidden {
		t.Errorf("no token: %d %q, want 403", code, body)
	}
	if code, body := rawRequest(t, addr, getWith(path, "X-Hoop-Token: wrong")); code != http.StatusForbidden {
		t.Errorf("wrong token: %d %q, want 403", code, body)
	}
	if code, body := rawRequest(t, addr, getWith(path, "X-Hoop-Token: "+goodToken)); code != http.StatusOK ||
		!strings.Contains(body, "PENDING") {
		t.Errorf("listed token: %d %q, want the review status", code, body)
	}
	if n := len(up.received()); n != 0 {
		t.Errorf("the upstream received %d reserved-route requests", n)
	}
}

// With the upstream written as a URL the lane also rewrites Host; the token
// is removed from the same head.
func TestACallerTokenLaneStripsTheTokenOnAnOriginLane(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, _, _, _ := tokenLane(t, up.URL, goodToken+"\n")
	if code, _ := rawRequest(t, addr, getWith("/v1/items", "X-Hoop-Token: "+goodToken)); code != http.StatusOK {
		t.Fatalf("a listed token got %d", code)
	}
	got := up.received()
	if len(got) != 1 || got[0].Get("X-Hoop-Token") != "" {
		t.Fatalf("the origin received %v, want one request without the token", got)
	}
}

// HTTP/2 is bridged into the HTTP/1 relay, so the same token check and the
// same strip apply to it.
func TestACallerTokenLaneChecksHTTP2(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, _, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	do := func(token string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/items", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("X-Hoop-Token", token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("h2 request: %v", err)
		}
		defer resp.Body.Close()
		if resp.ProtoMajor != 2 {
			t.Fatalf("spoke %s, want HTTP/2", resp.Proto)
		}
		return resp.StatusCode
	}
	if code := do(""); code != http.StatusForbidden {
		t.Errorf("h2 without a token: %d, want 403", code)
	}
	if code := do("wrong"); code != http.StatusForbidden {
		t.Errorf("h2 with a wrong token: %d, want 403", code)
	}
	if code := do(goodToken); code != http.StatusOK {
		t.Errorf("h2 with a listed token: %d, want 200", code)
	}
	got := up.received()
	if len(got) != 1 || got[0].Get("X-Hoop-Token") != "" {
		t.Errorf("the upstream received %v, want one request without the token", got)
	}
}

// The watcher polls on its own ticker, whoever owns the config, re-reads
// caller token and upstream credential files alike, and stops with the
// process.
func TestWatchFilesPollsUntilStopped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens")
	credPath := filepath.Join(dir, "api-token")
	old := time.Now().Add(-time.Hour)
	for p, content := range map[string]string{path: goodToken + "\n", credPath: upstreamToken} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	lanes := []lane{{name: "api", cfg: ListenerConfig{Protocol: "http",
		CallerToken:        &CallerTokenConfig{File: path, Name: "agents"},
		UpstreamCredential: &UpstreamCredentialConfig{Header: "Authorization", Scheme: "Bearer", File: credPath}}}}
	files, err := openCallerTokens(lanes, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := openUpstreamCredentials(lanes, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchFiles(ctx, slog.New(slog.DiscardHandler), []watchedFile{files[0], creds[0]}, 5*time.Millisecond)
		close(done)
	}()

	if err := os.WriteFile(path, []byte(newToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte(rotatedUpstreamToken), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, tok := lanes[0].upstreamCred.Current()
		if lanes[0].callerTokens.Match(newToken) && tok == rotatedUpstreamToken {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher never re-read the rotated files")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher did not stop with its context")
	}
}
