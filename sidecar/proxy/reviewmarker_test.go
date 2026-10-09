package proxy_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// fixedVerdict answers every statement with one verdict.
type fixedVerdict struct{ v policy.Verdict }

func (f fixedVerdict) Evaluate(inspect.Statement) policy.Verdict { return f.v }

// reviewDeny is a denial that names a review, as the analyzer's hold builds it.
func reviewDeny(review *policy.Review) policy.Verdict {
	v := policy.Deny("hold", "statement held for human approval")
	v.Review = review
	return v
}

// A client finds the review in the 403's headers, through a real relay, and
// a denial that names none still reads as a policy denial.
func TestAnHTTPReviewDenyCarriesTheReviewHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		want    http.Header
	}{
		{
			name:    "return mode pending",
			verdict: reviewDeny(&policy.Review{ID: "9f97", Status: "PENDING", Return: true}),
			want: http.Header{"X-Hoop-Denied": {"review"}, "X-Hoop-Review-Id": {"9f97"},
				"X-Hoop-Review-Status": {"PENDING"}, "X-Hoop-Approval-Id": {"9f97"},
				"X-Hoop-Approval-Status": {"PENDING"}, "Retry-After": {"5"}},
		},
		{
			name:    "hold mode rejected",
			verdict: reviewDeny(&policy.Review{ID: "9f97", Status: "REJECTED"}),
			want: http.Header{"X-Hoop-Denied": {"review"}, "X-Hoop-Review-Id": {"9f97"},
				"X-Hoop-Review-Status": {"REJECTED"}, "X-Hoop-Approval-Id": {"9f97"},
				"X-Hoop-Approval-Status": {"REJECTED"}, "Retry-After": nil},
		},
		{
			name:    "status unknown",
			verdict: reviewDeny(&policy.Review{ID: "9f97"}),
			want: http.Header{"X-Hoop-Denied": {"review"}, "X-Hoop-Review-Id": {"9f97"},
				"X-Hoop-Review-Status": nil, "X-Hoop-Approval-Id": {"9f97"},
				"X-Hoop-Approval-Status": nil, "Retry-After": nil},
		},
		{
			name:    "no review",
			verdict: policy.Deny("rule", "nope"),
			want: http.Header{"X-Hoop-Denied": {"policy"}, "X-Hoop-Review-Id": nil,
				"X-Hoop-Approval-Id": nil},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newEchoUpstream(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.HTTP,
				Connection: "api",
				Policy:     fixedVerdict{tc.verdict},
				DenyWriter: proxy.ProtocolDenyWriter{},
			})
			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if _, err := c.Write([]byte("DELETE /users/1 HTTP/1.1\r\nHost: h\r\n\r\n")); err != nil {
				t.Fatalf("write: %v", err)
			}
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
			for k, v := range tc.want {
				if got := resp.Header.Values(k); strings.Join(got, ",") != strings.Join(v, ",") {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		})
	}
}

// The id and status come from the control plane. A value that could break
// out of its header line is dropped, never written.
func TestAReviewHeaderRefusesAValueThatInjects(t *testing.T) {
	h := proxy.ReviewHeaders(policy.Review{ID: "9f97\r\nSet-Cookie: x=1", Status: "PENDING\n"})
	if h.Get("X-Hoop-Review-Id") != "" || h.Get("X-Hoop-Review-Status") != "" ||
		h.Get("X-Hoop-Approval-Id") != "" || h.Get("X-Hoop-Approval-Status") != "" {
		t.Errorf("an unsafe value reached a header: %v", h)
	}
	frame := string(proxy.HTTPReviewForbidden("held", policy.Review{ID: "9f97\r\nSet-Cookie: x=1"}))
	if strings.Contains(frame, "Set-Cookie") {
		t.Errorf("the response carries an injected header: %q", frame)
	}
}

// A route the lane answers itself is answered before identity and policy,
// never reaches the upstream, and is not a denial.
func TestALaneAnswerNeverReachesTheUpstream(t *testing.T) {
	up := newEchoUpstream(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	reply := []byte("HTTP/1.1 200 OK\r\nContent-Length: 3\r\nConnection: close\r\n\r\nok\n")
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		Connection: "api",
		// A lane that denies everything still answers its own route.
		Policy:     fixedVerdict{policy.Deny("rule", "nope")},
		DenyWriter: proxy.ProtocolDenyWriter{},
		Answer: func(stmt inspect.Statement) func(context.Context) []byte {
			if stmt.HTTP.Path != "/.well-known/hoop/reviews/x" {
				return nil
			}
			return func(context.Context) []byte { return reply }
		},
	})
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(reply) {
		t.Errorf("the client read %q, want the lane's own reply", got)
	}
	if n := len(up.got()); n != 0 {
		t.Errorf("the upstream received %d bytes of a request the lane answered", n)
	}
	if _, _, denied := srv.Stats(); denied != 0 {
		t.Errorf("the answer counted %d denials, want none", denied)
	}
}

// A reserved route pipelined behind another request ends the connection with
// nothing written, so no reply can overtake the earlier response.
func TestAPipelinedLaneAnswerHangsUp(t *testing.T) {
	up := newEchoUpstream(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		Connection: "api",
		DenyWriter: proxy.ProtocolDenyWriter{},
		Answer: func(stmt inspect.Statement) func(context.Context) []byte {
			if stmt.HTTP.Path != "/.well-known/hoop/reviews/x" {
				return nil
			}
			return func(context.Context) []byte { return []byte("HTTP/1.1 200 OK\r\n\r\n") }
		},
	})
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n" +
		"GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the client read %q, want the connection closed unanswered", got)
	}
	if n := len(up.got()); n != 0 {
		t.Errorf("the upstream received %d bytes", n)
	}
}
