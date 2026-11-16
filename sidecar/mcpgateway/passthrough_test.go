package mcpgateway_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/mcpgateway"
	"github.com/hoophq/hoop/sidecar/mcpgateway/internal/testupstream"
)

const passthroughHeader = "X-Upstream-Token"

// startPassthroughLane serves a loopback lane with no caller auth that forwards
// each caller's own credential, in front of a recorded testupstream server.
func startPassthroughLane(t *testing.T, ln *testLane, caller *daemon.MCPCallerCheck, logs io.Writer) (string, *recordedUpstream) {
	t.Helper()
	return startPassthroughLaneOn(t, ln, testupstream.Handler(testupstream.NewServer("")), caller, logs)
}

// startPassthroughLaneOn is startPassthroughLane in front of upstream.
func startPassthroughLaneOn(t *testing.T, ln *testLane, upstream http.Handler, caller *daemon.MCPCallerCheck, logs io.Writer) (string, *recordedUpstream) {
	t.Helper()
	rec := &recordedUpstream{}
	up := httptest.NewServer(rec.wrap(upstream))
	t.Cleanup(up.Close)
	if logs == nil {
		logs = io.Discard
	}
	_, url := serveLane(t, daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: up.URL + "/mcp", Transport: daemon.MCPTransportStreamableHTTP,
		Policy: daemon.MCPGatewayConfig{PassthroughHeader: passthroughHeader},
		Open:   ln.open, Caller: caller,
		Log: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return url, rec
}

func withUpstreamToken(values ...string) http.Header {
	h := http.Header{}
	for _, v := range values {
		h.Add(passthroughHeader, v)
	}
	return h
}

// Every upstream request carries the caller's own credential as a bearer, and
// the passthrough header never reaches the upstream.
func TestEachCallerReachesTheUpstreamWithItsOwnCredential(t *testing.T) {
	ln := &testLane{}
	url, up := startPassthroughLane(t, ln, nil, nil)
	for _, tc := range []struct{ sent, want string }{
		{"Bearer alice-upstream-pat", "Bearer alice-upstream-pat"},
		{"bob-upstream-pat", "Bearer bob-upstream-pat"},
	} {
		before, _ := up.all()
		c := &client{url: url, header: withUpstreamToken(tc.sent)}
		c.init(t)
		if body := c.call(t, 3, "echo", `{"message":"hi"}`); !strings.Contains(body, "hi") {
			t.Fatalf("call: %s", body)
		}
		up.mu.Lock()
		reqs := append([]string(nil), up.reqs[before:]...)
		up.mu.Unlock()
		if len(reqs) == 0 {
			t.Fatal("no upstream request")
		}
		for _, r := range reqs {
			if !strings.Contains(r, "Authorization: "+tc.want+"\r\n") {
				t.Errorf("an upstream request lacks %q:\n%s", tc.want, r)
			}
			if strings.Contains(strings.ToLower(r), strings.ToLower(passthroughHeader)) {
				t.Errorf("the passthrough header reached the upstream:\n%s", r)
			}
		}
	}
}

// A request without the header, with an empty one or with two is refused with
// 403 before mcpproxy reads it, and the message names the header.
func TestARequestWithoutThePassthroughHeaderIsRefused(t *testing.T) {
	ln := &testLane{}
	url, up := startPassthroughLane(t, ln, nil, nil)
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"no header", nil, "request refused: the request carried no " + passthroughHeader + " header, the caller's own credential for the MCP server"},
		{"empty", []string{""}, "request refused: the request carried no " + passthroughHeader + " header, the caller's own credential for the MCP server"},
		{"bearer only", []string{"Bearer  "}, "request refused: the request carried no " + passthroughHeader + " header, the caller's own credential for the MCP server"},
		{"repeated", []string{"a", "b"}, "request refused: the request carried more than one " + passthroughHeader + " header"},
	} {
		c := &client{url: url, header: withUpstreamToken(tc.values...)}
		if code, body := c.post(t, initializeBody); code != http.StatusForbidden || strings.TrimSpace(body) != tc.want || c.sid != "" {
			t.Errorf("%s: %d %q, want 403 %q", tc.name, code, body, tc.want)
		}
	}
	ok := &client{url: url, header: withUpstreamToken("good")}
	ok.init(t)
	if code := (&client{url: url, sid: ok.sid}).delete(t); code != http.StatusForbidden {
		t.Errorf("DELETE without the header: %d, want 403", code)
	}
	if n, _ := up.all(); ln.count() != 1 || n == 0 {
		t.Fatalf("%d sessions and %d upstream requests, want 1 session from the good request", ln.count(), n)
	}
}

// The credential reaches neither the logs nor anything the engine hands the
// daemon: the caller, statements, catalog, activities and masked results. The
// daemon builds the audit trail, the policy and OPA input and the analyzer's
// prompt from those alone. sidecar/e2e checks the same on the shipped binary.
func TestThePassthroughCredentialNeverLeavesTheEngine(t *testing.T) {
	const secret = "ghp_passthrough-secret-0001"
	ln := &testLane{deny: map[string]string{"delete_customer": "no-delete"}}
	logs := &lockedBuffer{}
	url, _ := startPassthroughLane(t, ln, nil, logs)
	c := &client{url: url, header: withUpstreamToken("Bearer " + secret)}
	c.init(t)
	c.list(t)
	if body := c.call(t, 3, "echo", `{"message":"hi"}`); !strings.Contains(body, "hi") {
		t.Fatalf("call: %s", body)
	}
	if body := c.call(t, 4, "delete_customer", `{"id":"7"}`); !strings.Contains(body, "denied by the hoop sidecar") {
		t.Fatalf("denied call: %s", body)
	}
	if code, _ := (&client{url: url}).post(t, initializeBody); code != http.StatusForbidden {
		t.Fatalf("no header: %d", code)
	}
	if code := c.delete(t); code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", code)
	}
	waitFor(t, "the session to close", func() bool { _, _, closed := ln.session(0).snapshot(); return closed })

	if !strings.Contains(logs.String(), "mcp request refused") {
		t.Fatalf("nothing to check: logs %q", logs.String())
	}
	ln.mu.Lock()
	callers := fmt.Sprintf("%+v", ln.callers)
	ln.mu.Unlock()
	for where, text := range map[string]string{
		"the logs":            logs.String(),
		"the sidecar caller":  callers,
		"the sidecar session": ln.session(0).everything(),
	} {
		if strings.Contains(text, secret) {
			t.Errorf("%s holds the credential:\n%s", where, text)
		}
	}
}

// Behind caller_token, the caller token is checked first, and each header is
// removed: the upstream gets only the passthrough credential.
func TestPassthroughBehindACallerToken(t *testing.T) {
	ln := &testLane{}
	url, up := startPassthroughLane(t, ln, tokenCheck(), nil)
	h := withToken("good-token")
	h.Set(passthroughHeader, "upstream-pat")
	if code, body := (&client{url: url, header: withUpstreamToken("upstream-pat")}).post(t, initializeBody); code != http.StatusForbidden || !strings.Contains(body, "carried no credential") {
		t.Fatalf("no caller token: %d %s", code, body)
	}
	c := &client{url: url, header: h}
	c.init(t)
	c.call(t, 3, "echo", `{"message":"hi"}`)
	_, all := up.all()
	if !strings.Contains(all, "Authorization: Bearer upstream-pat\r\n") || strings.Contains(all, "good-token") {
		t.Fatalf("upstream requests:\n%s", all)
	}
	if ln.callers[0].Subject != "agents" {
		t.Fatalf("caller %q, want the resolved name", ln.callers[0].Subject)
	}
}

// A later request on a session uses its own credential, not the one that
// opened the session.
func TestEachRequestOnASessionUsesItsOwnCredential(t *testing.T) {
	ln := &testLane{}
	url, up := startPassthroughLane(t, ln, nil, nil)
	c := &client{url: url, header: withUpstreamToken("first-pat")}
	c.init(t)
	c.header = withUpstreamToken("second-pat")
	before, _ := up.all()
	c.call(t, 3, "echo", `{"message":"hi"}`)
	up.mu.Lock()
	reqs := strings.Join(up.reqs[before:], "\n")
	up.mu.Unlock()
	if !strings.Contains(reqs, "Authorization: Bearer second-pat\r\n") || strings.Contains(reqs, "first-pat") {
		t.Fatalf("the call reached the upstream with:\n%s", reqs)
	}
}

// echoingUpstream quotes the Authorization it got: in the challenge of a 401 to
// a credential holding "deny-me", and in the body of a 500 to a tools/call.
func echoingUpstream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		auth := r.Header.Get("Authorization")
		switch {
		case strings.Contains(auth, "deny-me"):
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="`+auth+`"`)
			http.Error(w, "bad credential: "+auth, http.StatusUnauthorized)
		case strings.Contains(string(body), `"tools/call"`):
			http.Error(w, "upstream failed for "+auth, http.StatusInternalServerError)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// An upstream error body that quotes the credential reaches the log with the
// credential redacted, on initialize and in a session.
func TestAnUpstreamErrorBodyDoesNotLogTheCredential(t *testing.T) {
	const refused, failed = "deny-me-secret-0001", "ghp_quoted-secret-0002"
	ln := &testLane{}
	logs := &lockedBuffer{}
	url, _ := startPassthroughLaneOn(t, ln, echoingUpstream(testupstream.Handler(testupstream.NewServer(""))), nil, logs)

	if _, body := (&client{url: url, header: withUpstreamToken("Bearer " + refused)}).post(t, initializeBody); !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("initialize with a refused credential: %s", body)
	}
	c := &client{url: url, header: withUpstreamToken(failed)}
	c.init(t)
	if body := c.call(t, 3, "echo", `{"message":"hi"}`); !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("a call the upstream fails: %s", body)
	}
	text := logs.String()
	if strings.Count(text, "[REDACTED]") < 2 {
		t.Fatalf("nothing to check: the upstream errors are not in the log:\n%s", text)
	}
	for _, secret := range []string{refused, failed} {
		if strings.Contains(text, secret) {
			t.Errorf("the log holds %q:\n%s", secret, text)
		}
	}
}

// The engine refuses a passthrough lane on sse, which validate also refuses.
func TestPassthroughOnSSEIsRefused(t *testing.T) {
	_, err := mcpgateway.Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/sse", Transport: daemon.MCPTransportSSE,
		Policy: daemon.MCPGatewayConfig{Transport: daemon.MCPTransportSSE, PassthroughHeader: passthroughHeader},
	})
	if err == nil || !strings.Contains(err.Error(), "streamable-http") {
		t.Fatalf("Build: %v", err)
	}
}
