package proxy_test

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// A request whose body arrives in more than one read used to reach the
// upstream in part before the gate judged it: the head and the start of the
// body went out, and a denial could not recall them. A route that acts on
// its head alone then ran a request policy refused.

func noDeletes(t *testing.T) *policy.Rules {
	t.Helper()
	r, err := policy.NewRules([]policy.Rule{{
		Name:    "no-deletes",
		Type:    policy.MatchPattern,
		Pattern: `^DELETE `,
		Message: "deletes are refused on this lane",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return r
}

// writeParts writes each part on its own, pausing between them so the relay
// reads them apart, and fails the test if the upstream received anything
// before the last part was written.
func writeParts(t *testing.T, c net.Conn, up *echoUpstream, parts []string, before string) {
	t.Helper()
	for i, p := range parts {
		if _, err := c.Write([]byte(p)); err != nil {
			t.Fatalf("write part %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
		if i < len(parts)-1 {
			if got := viaLine.ReplaceAllString(string(up.got()), ""); got != before {
				t.Fatalf("after part %d the upstream received %q, want %q: the request left before it was judged", i, got, before)
			}
		}
	}
}

func TestADeniedRequestSplitAcrossReadsReachesNoUpstream(t *testing.T) {
	const get = "GET /items HTTP/1.1\r\nHost: h\r\n\r\n"
	for name, tc := range map[string]struct {
		parts  []string
		before string // what an earlier, allowed request put upstream
	}{
		"content-length": {parts: []string{
			"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 9\r\n\r\n{\"wh",
			"y\":1}",
		}},
		"chunked": {parts: []string{
			"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n4\r\n{\"wh\r\n",
			"5\r\ny\":1}\r\n0\r\n\r\n",
		}},
		"head alone first": {parts: []string{
			"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 9\r\n\r\n",
			"{\"why\":1}",
		}},
		"behind an allowed request": {parts: []string{
			get + "DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 9\r\n\r\n{",
			"\"why\":1}",
		}, before: get},
	} {
		t.Run(name, func(t *testing.T) {
			up := newEchoUpstream(t, []byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
			sink := audit.NewMemorySink(64)
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.HTTP,
				Connection: "api",
				Policy:     noDeletes(t),
				Audit:      sink,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})
			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()

			writeParts(t, c, up, tc.parts, tc.before)

			if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			answer, _ := io.ReadAll(c)
			if !strings.Contains(string(answer), "403 Forbidden") ||
				!strings.Contains(string(answer), "deletes are refused on this lane") {
				t.Fatalf("the client read %q, want the rule's 403", answer)
			}
			if got := viaLine.ReplaceAllString(string(up.got()), ""); got != tc.before {
				t.Errorf("the upstream received %q, want %q: a denied request reached it", got, tc.before)
			}
			var violations int
			for _, ev := range sink.Events() {
				if ev.Kind == audit.KindViolation && ev.Rule == "no-deletes" {
					violations++
				}
			}
			if violations != 1 {
				t.Errorf("violations recorded = %d, want 1", violations)
			}
		})
	}
}

// The hold must not cost an allowed request any byte: it leaves whole, in
// order, once its last byte arrived.
func TestAnAllowedRequestSplitAcrossReadsArrivesWhole(t *testing.T) {
	up := newEchoUpstream(t, []byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		Connection: "api",
		Policy:     noDeletes(t),
		DenyWriter: proxy.ProtocolDenyWriter{},
	})
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	const body = `{"name":"widget"}`
	head := "POST /items HTTP/1.1\r\nHost: h\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	writeParts(t, c, up, []string{head, body[:5], body[5:]}, "")

	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, _ := c.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 204") {
		t.Fatalf("the client read %q, want the upstream's 204", buf[:n])
	}
	if got := viaLine.ReplaceAllString(string(up.got()), ""); got != head+body {
		t.Errorf("the upstream received %q, want %q", got, head+body)
	}
}

// A client sending Expect: 100-continue waits for the 100 before its body.
// The held head never reaches the upstream, so no 100 comes; the client
// sends the body when its own wait ends, and the request completes.
func TestAnExpectContinueRequestCompletesAfterTheClientsWait(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(), Protocol: inspect.HTTP,
		Policy: noDeletes(t), DenyWriter: proxy.ProtocolDenyWriter{},
	})
	tr := &http.Transport{ExpectContinueTimeout: 200 * time.Millisecond}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr().String()+"/items", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Expect", "100-continue")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if n := len(up.requests()); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1", n)
	}
}

// HTTP/2 reaches the same relay re-serialized, and the bridge flushes a
// request's head before its body. The upstream's handler starts when the
// head arrives, so it must not start for a request policy refuses.
func TestADeniedH2RequestWithASlowBodyReachesNoUpstream(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(), Protocol: inspect.HTTP,
		Policy: noDeletes(t), DenyWriter: proxy.ProtocolDenyWriter{},
	})
	c := h2cClient(t, nil, false)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodDelete, "http://"+srv.Addr().String()+"/items/1", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 9
	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := c.Do(req)
		done <- result{resp, err}
	}()

	if _, err := pw.Write([]byte(`{"wh`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := len(up.requests()); n != 0 {
		t.Fatalf("the upstream started %d requests before the body arrived", n)
	}
	if _, err := pw.Write([]byte(`y":1}`)); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("request: %v", r.err)
		}
		defer r.resp.Body.Close()
		if r.resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", r.resp.StatusCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the denied request never got an answer")
	}
	if n := len(up.requests()); n != 0 {
		t.Errorf("the upstream started %d requests for a denied one", n)
	}
}
