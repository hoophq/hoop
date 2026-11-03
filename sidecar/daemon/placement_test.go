package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
)

// trailHolds reports whether any event of the trail holds s, in any field.
func trailHolds(t *testing.T, sink *sshTestSink, s string) bool {
	t.Helper()
	raw, err := json.Marshal(sink.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), s)
}

// placementRefusals counts the trail's placement refusals of request.
func placementRefusals(sink *sshTestSink, request string) int {
	n := 0
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindViolation && ev.Rule == "credential-placement" && ev.Statement == request {
			n++
		}
	}
	return n
}

// ENG-871 on a caller_token lane: OpenAI's vault fills the caller token
// into the URL or the body when the agent writes the placeholder there. The
// request is refused before the upstream sees it, and the trail never
// holds the token.
func TestACallerTokenOutsideItsHeaderIsRefusedAndNeverAudited(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, sink, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	header := "X-Hoop-Token: " + goodToken
	for name, head := range map[string]string{
		"query": getWith("/v1/echo?t="+goodToken, header),
		"form body": "POST /v1/echo HTTP/1.1\r\nHost: api.internal\r\n" + header +
			"\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 24\r\nConnection: close\r\n\r\nt=" + goodToken,
	} {
		t.Run(name, func(t *testing.T) {
			code, body := rawRequest(t, addr, head)
			if code != http.StatusForbidden || !strings.Contains(body, "outside the header it belongs in") ||
				strings.Contains(body, goodToken) {
				t.Errorf("answer = %d %q, want the placement 403", code, body)
			}
		})
	}
	// Long enough for a forwarded request to have reached the handler.
	time.Sleep(100 * time.Millisecond)
	if n := len(up.received()); n != 0 {
		t.Fatalf("the upstream received %d requests", n)
	}
	if got := placementRefusals(sink, "GET /v1/echo") + placementRefusals(sink, "POST /v1/echo"); got != 2 {
		t.Errorf("%d placement refusals, want 2", got)
	}
	if trailHolds(t, sink, goodToken) {
		t.Error("the trail holds the caller token")
	}
}

// ENG-871 on the Kubernetes template's lane, which admits by no caller
// token: the agent's bearer is the token the vault fills. Once the upstream
// accepted it, a request on another connection that leaves Authorization
// off and writes the token into the path is refused, and never reaches an
// upstream that would echo it (`pods "<token>" not found`).
func TestAnHTTPLaneKeepsAnAcceptedBearerOutOfAPath(t *testing.T) {
	const bearer = "kube-sa-token-0123456789abcdef"
	var received atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+bearer {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(up.Close)

	ln := lane{name: "kube", cfg: ListenerConfig{Name: "kube", Protocol: "http",
		Listen: "127.0.0.1:0", Upstream: strings.TrimPrefix(up.URL, "http://")}}
	ln.codecFactory = laneCodecFactory(ln.cfg, nil)
	sink := &sshTestSink{}
	srv, err := buildServer(ln, AuditConfig{}, sink, pendingReviews(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	addr := waitForAddr(t, srv)

	if code, _ := rawRequest(t, addr, getWith("/api/v1/namespaces/default/pods", "Authorization: Bearer "+bearer)); code != http.StatusOK {
		t.Fatalf("the authorized request = %d, want 200", code)
	}
	code, body := rawRequest(t, addr, getWith("/api/v1/namespaces/default/pods/"+bearer))
	if code != http.StatusForbidden || strings.Contains(body, bearer) {
		t.Fatalf("the leaking request = %d %q, want the placement 403", code, body)
	}
	time.Sleep(100 * time.Millisecond)
	if n := received.Load(); n != 1 {
		t.Errorf("the upstream received %d requests, want only the authorized one", n)
	}
	if placementRefusals(sink, "GET /api/v1/namespaces/default/pods/[redacted]") != 1 {
		t.Errorf("no placement refusal of the redacted path in %+v", sink.snapshot())
	}
	if trailHolds(t, sink, bearer) {
		t.Error("the trail holds the bearer")
	}
}

// Over HTTP/2 the bridge writes each request into the relay as HTTP/1, so
// the same filter sees it: a caller token in an h2 request's body is
// refused too, and never reaches the upstream.
func TestACallerTokenInAnHTTP2BodyIsRefused(t *testing.T) {
	up := newRecordingUpstream(t)
	addr, sink, _, _ := tokenLane(t, strings.TrimPrefix(up.URL, "http://"), goodToken+"\n")
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p}
	t.Cleanup(tr.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/echo", strings.NewReader(`{"t":"`+goodToken+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Hoop-Token", goodToken)
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("h2 request: %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusForbidden {
		t.Errorf("answer = %s %d, want an h2 403", resp.Proto, resp.StatusCode)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(up.received()); n != 0 {
		t.Fatalf("the upstream received %d requests", n)
	}
	if trailHolds(t, sink, goodToken) {
		t.Error("the trail holds the caller token")
	}
}
