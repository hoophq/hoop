package proxy_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// waitingPolicy stands in for a hold: it blocks every statement until the
// connection's context ends, then denies with the cause, the way the
// analyzer's hold does.
type waitingPolicy struct {
	started chan struct{}
	ended   chan error
}

func newWaitingPolicy() *waitingPolicy {
	return &waitingPolicy{started: make(chan struct{}, 1), ended: make(chan error, 1)}
}

func (p *waitingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	return p.EvaluateWith(stmt, nil)
}

func (p *waitingPolicy) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	p.started <- struct{}{}
	if ec == nil || ec.ConnCtx == nil {
		p.ended <- nil
		return policy.Deny("hold", "no connection context reached the policy")
	}
	select {
	case <-ec.ConnCtx.Done():
	case <-time.After(10 * time.Second):
		p.ended <- nil
		return policy.Deny("hold", "the wait never ended")
	}
	cause := context.Cause(ec.ConnCtx)
	p.ended <- cause
	return policy.Deny("hold", "the connection ended while waiting: "+cause.Error())
}

// cancelHonoringSink refuses a write under an ended context, as the SQLite
// store does. The hold's record is written after the connection ended, so
// this is the sink that would lose it.
type cancelHonoringSink struct{ *audit.MemorySink }

func (s cancelHonoringSink) Write(ctx context.Context, ev audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemorySink.Write(ctx, ev)
}

// A client that hangs up mid-hold ends the wait at once, and the statement's
// record still lands with the outcome and the cause. Without the read-ahead
// nothing reads the client socket while the pump sits in the gate, and the
// hold would run to its budget.
func TestAHoldEndsWhenTheClientHangsUp(t *testing.T) {
	up := newEchoUpstream(t, nil)
	pol := newWaitingPolicy()
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     pol,
		Audit:      cancelHonoringSink{sink},
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write(pgQuery("DELETE FROM customers")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-pol.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement never reached the policy")
	}
	c.Close()

	select {
	case cause := <-pol.ended:
		if cause == nil || !strings.Contains(cause.Error(), "the client closed the connection") {
			t.Errorf("the wait ended with %v, want the client hangup as its cause", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the hold outlived the client")
	}

	if got := up.got(); len(got) != 0 {
		t.Errorf("upstream received %d bytes from a statement whose client left", len(got))
	}
	waitForViolation(t, sink, "the client closed the connection")
}

// The upstream going away ends the wait too, before anything is claimed: the
// statement could not run on a dead socket, and the approval stays unspent
// for the retry.
func TestAHoldEndsWhenTheUpstreamCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	upConns := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			upConns <- c
		}
	}()

	pol := newWaitingPolicy()
	srv := startServer(t, proxy.Config{
		Upstream:   ln.Addr().String(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     pol,
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(pgQuery("DELETE FROM customers")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-pol.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement never reached the policy")
	}
	(<-upConns).Close()

	select {
	case cause := <-pol.ended:
		if cause == nil || !strings.Contains(cause.Error(), "the upstream closed the connection") {
			t.Errorf("the wait ended with %v, want the upstream close as its cause", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the hold outlived the upstream")
	}
}

// decidingPolicy stands in for a hold that settles: it blocks every request
// until the test sends the verdict a reviewer would. Responses pass.
type decidingPolicy struct {
	started chan struct{}
	verdict chan policy.Verdict
}

func (p *decidingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	if stmt.Direction != inspect.FromClient {
		return policy.Verdict{}
	}
	p.started <- struct{}{}
	return <-p.verdict
}

// An http request held for review reaches the upstream only once released,
// and a refusal reaches the caller as a 403 that names it.
func TestAnHTTPHoldForwardsOnlyWhatIsReleased(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		want    string
		forward bool
	}{
		{name: "approved", verdict: policy.Verdict{}, want: "200 OK", forward: true},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected"), want: "403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newEchoUpstream(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
			pol := &decidingPolicy{started: make(chan struct{}, 1), verdict: make(chan policy.Verdict)}
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.HTTP,
				Connection: "api",
				Policy:     pol,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})

			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			req := "POST /transfers HTTP/1.1\r\nHost: h\r\nContent-Length: 2\r\n\r\n{}"
			if _, err := c.Write([]byte(req)); err != nil {
				t.Fatalf("write: %v", err)
			}
			select {
			case <-pol.started:
			case <-time.After(3 * time.Second):
				t.Fatal("the request never reached the policy")
			}
			if got := up.got(); len(got) != 0 {
				t.Fatalf("upstream received %d bytes while the request was held", len(got))
			}
			pol.verdict <- tc.verdict

			if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatalf("set deadline: %v", err)
			}
			buf := make([]byte, 512)
			n, err := c.Read(buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := string(buf[:n]); !strings.Contains(got, tc.want) {
				t.Errorf("the client read %q, want %q", got, tc.want)
			}
			if forwarded := len(up.got()) > 0; forwarded != tc.forward {
				t.Errorf("upstream received the request: %v, want %v", forwarded, tc.forward)
			}
		})
	}
}

// ClickHouse native packets at revision 54450 (the codec's PinRevision),
// encoded once with github.com/ClickHouse/ch-go. Checked in as bytes so this
// module takes no second direct dependency.
const (
	chClientHelloHex = "000b746573742d636c69656e740101b2a90305617070646207617070757365720761707070617373"
	chServerHelloHex = "000a436c69636b486f7573651903b2a903000000"
	chDeleteQueryHex = "0103712d31010000000000000000000000010000000000b2a90300000000000002001f" +
		"44454c4554452046524f4d206f7264657273205748455245206964203d2037"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return b
}

// A ClickHouse query held for review reaches the upstream only once released,
// and a refusal reaches the client as a native ACCESS_DENIED exception.
func TestAClickHouseHoldForwardsOnlyWhatIsReleased(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		forward bool
	}{
		{name: "approved", verdict: policy.Verdict{}, forward: true},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hello, query := mustHex(t, chClientHelloHex), mustHex(t, chDeleteQueryHex)
			up := newEchoUpstream(t, mustHex(t, chServerHelloHex))
			pol := &decidingPolicy{started: make(chan struct{}, 1), verdict: make(chan policy.Verdict)}
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.ClickHouse,
				Connection: "warehouse",
				Policy:     pol,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})

			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("set deadline: %v", err)
			}
			if _, err := c.Write(hello); err != nil {
				t.Fatalf("write hello: %v", err)
			}
			if _, err := io.ReadFull(c, make([]byte, len(mustHex(t, chServerHelloHex)))); err != nil {
				t.Fatalf("read server hello: %v", err)
			}
			if _, err := c.Write(query); err != nil {
				t.Fatalf("write query: %v", err)
			}
			select {
			case <-pol.started:
			case <-time.After(3 * time.Second):
				t.Fatal("the query never reached the policy")
			}
			if got := up.got(); !bytes.Equal(got, hello) {
				t.Fatalf("upstream received %d bytes past the hello while the query was held", len(got)-len(hello))
			}
			pol.verdict <- tc.verdict

			if !tc.forward {
				got, err := io.ReadAll(c)
				if err != nil {
					t.Fatalf("read denial after %d bytes: %v", len(got), err)
				}
				code, n := binary.Uvarint(got)
				if n <= 0 || code != 2 || len(got) < n+4 || binary.LittleEndian.Uint32(got[n:n+4]) != 497 {
					t.Errorf("the client read %x, want a ClickHouse ACCESS_DENIED exception", got)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && tc.forward && !bytes.Contains(up.got(), query) {
				time.Sleep(10 * time.Millisecond)
			}
			if forwarded := bytes.Contains(up.got(), query); forwarded != tc.forward {
				t.Errorf("upstream received the query: %v, want %v", forwarded, tc.forward)
			}
		})
	}
}

// The read-ahead hands chunks over in two alternating buffers. Traffic that
// spans many reads must arrive byte for byte, or the reader overwrote a chunk
// the pump was still forwarding.
func TestReadAheadRelaysEveryByte(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	go func() { _, _ = io.Copy(io.Discard, c) }()

	var want bytes.Buffer
	for i := range 400 {
		want.Write(pgQuery("SELECT " + strings.Repeat("x", i*7%900) + " FROM customers"))
	}
	if _, err := c.Write(want.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(up.got()) < want.Len() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Equal(up.got(), want.Bytes()) {
		t.Fatalf("upstream received %d bytes that differ from the %d sent", len(up.got()), want.Len())
	}
}

func waitForViolation(t *testing.T, sink *audit.MemorySink, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var found int
		for _, ev := range sink.Events() {
			if ev.Kind == audit.KindViolation {
				found++
				if !strings.Contains(ev.Message, want) {
					t.Errorf("the record says %q, want it to carry %q", ev.Message, want)
				}
			}
		}
		if found > 1 {
			t.Fatalf("the statement was recorded %d times, want once", found)
		}
		if found == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the held statement left no record")
}

// highRisk rates every statement high, so a lane mapping high to
// require_review holds it.
type highRisk struct{}

func (highRisk) Name() string { return "stub" }

func (highRisk) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh}, nil
}

// pendingReviewer leaves every review PENDING and signals each filing.
type pendingReviewer struct{ filed chan struct{} }

func (r pendingReviewer) File(context.Context, string) (analyzer.ReviewResult, error) {
	select {
	case r.filed <- struct{}{}:
	default:
	}
	return analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}, nil
}

func (pendingReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{ID: id, Status: "PENDING"}, nil
}

// pgStartup builds a v3 StartupMessage with the application_name psql sends.
func pgStartup(applicationName string) []byte {
	var params []byte
	for _, kv := range [][2]string{{"user", "agent"}, {"database", "appdb"}, {"application_name", applicationName}} {
		params = append(params, kv[0]...)
		params = append(params, 0)
		params = append(params, kv[1]...)
		params = append(params, 0)
	}
	params = append(params, 0)
	out := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(out[4:8], 3<<16)
	return append(out, params...)
}

// On a hold lane, a Postgres client that opts into return in its
// application_name is denied at once, and one that does not is held.
func TestAPostgresClientOptsIntoReturnWithItsApplicationName(t *testing.T) {
	for _, tc := range []struct {
		appName string
		held    bool
	}{
		{appName: "my-agent hoop-review=return"},
		{appName: "psql", held: true},
	} {
		t.Run(tc.appName, func(t *testing.T) {
			rev := pendingReviewer{filed: make(chan struct{}, 1)}
			ev, err := analyzer.New(analyzer.Config{
				Rule:     "payments",
				Provider: highRisk{},
				Trigger:  analyzer.Trigger{Operations: []inspect.Operation{inspect.OpDelete}},
				Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
				Review:   rev,
			})
			if err != nil {
				t.Fatalf("analyzer.New: %v", err)
			}
			up := newEchoUpstream(t, nil)
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.Postgres,
				Connection: "appdb",
				Policy:     ev,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})

			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("set deadline: %v", err)
			}
			if _, err := c.Write(append(pgStartup(tc.appName), pgQuery("DELETE FROM customers")...)); err != nil {
				t.Fatalf("write: %v", err)
			}

			select {
			case <-rev.filed:
			case <-time.After(3 * time.Second):
				t.Fatal("the statement was not filed for review")
			}
			if tc.held {
				// A held statement gets no answer until the review settles.
				if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
					t.Fatalf("set deadline: %v", err)
				}
				var ne net.Error
				if n, err := c.Read(make([]byte, 64)); !errors.As(err, &ne) || !ne.Timeout() {
					t.Fatalf("the client read %d bytes (err=%v), want the statement held", n, err)
				}
			} else {
				got, err := io.ReadAll(c)
				if err != nil {
					t.Fatalf("read the denial: %v", err)
				}
				if !strings.Contains(string(got), "resend") {
					t.Errorf("the client read %q, want an immediate return denial", got)
				}
			}
			if bytes.Contains(up.got(), []byte("DELETE")) {
				t.Error("the upstream received the denied statement")
			}
		})
	}
}

// namingReviewer releases every filing at once, as an approval the plane
// consumed, and keeps the caller each filing's context carried: what the
// daemon sends the plane as the requester.
type namingReviewer struct {
	filed chan session.Identity
}

func newNamingReviewer() *namingReviewer {
	return &namingReviewer{filed: make(chan session.Identity, 8)}
}

func (r *namingReviewer) File(ctx context.Context, _ string) (analyzer.ReviewResult, error) {
	id, ok := session.IdentityFromContext(ctx)
	if !ok {
		id = session.Identity{Subject: "<no caller on the context>"}
	}
	r.filed <- id
	return analyzer.ReviewResult{ID: "9f97", Status: "EXECUTED", Forward: true}, nil
}

func (*namingReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{ID: id, Status: "PENDING"}, nil
}

func (r *namingReviewer) next(t *testing.T) session.Identity {
	t.Helper()
	select {
	case id := <-r.filed:
		return id
	case <-time.After(3 * time.Second):
		t.Fatal("the statement was not filed for review")
	}
	return session.Identity{}
}

func holdingEvaluator(t *testing.T, rev analyzer.Reviewer, trigger analyzer.Trigger) *analyzer.Evaluator {
	t.Helper()
	ev, err := analyzer.New(analyzer.Config{
		Rule:     "payments",
		Provider: highRisk{},
		Trigger:  trigger,
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		Review:   rev,
	})
	if err != nil {
		t.Fatalf("analyzer.New: %v", err)
	}
	return ev
}

// A pgwire lane names its caller from the StartupMessage's user, and a review
// filed on it must say so: the name is the one the client CLAIMED, marked
// database_user, with the address it came from. Filed before any
// AuthenticationOk here, which is exactly the case the label exists for.
func TestAHeldPostgresStatementNamesItsCaller(t *testing.T) {
	rev := newNamingReviewer()
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     holdingEvaluator(t, rev, analyzer.Trigger{Operations: []inspect.Operation{inspect.OpDelete}}),
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(append(pgStartup("psql"), pgQuery("DELETE FROM customers")...)); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := rev.next(t)
	if got.Subject != "agent" || got.Method != session.MethodDatabaseUser {
		t.Errorf("the review named %q via %q, want agent via %s", got.Subject, got.Method, session.MethodDatabaseUser)
	}
	if got.PeerAddr == "" {
		t.Error("the review names no peer address")
	}
}

// Envoy pools one upstream connection across users. A review filed for
// bob's request must name bob, not alice, whose request opened the
// connection: the caller rides per statement, after the gate rotated the
// session.
func TestAnHTTPHoldNamesEachCallerOnAPooledConnection(t *testing.T) {
	const ok = "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"
	rev := newNamingReviewer()
	up := newEchoUpstream(t, []byte(ok))
	cfg := identityLane(up.addr(), nil)
	cfg.Policy = holdingEvaluator(t, rev, analyzer.Trigger{All: true})
	cfg.DenyWriter = proxy.ProtocolDenyWriter{}
	srv := startServer(t, cfg)

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	for _, user := range []string{"alice@example.com", "bob@example.com"} {
		req := "DELETE /transfers/7 HTTP/1.1\r\nHost: api\r\nX-Forwarded-User: " + user + "\r\n\r\n"
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := rev.next(t)
		if got.Subject != user || got.Method != session.MethodIdentityHeader {
			t.Errorf("the review for %s named %q via %q", user, got.Subject, got.Method)
		}
		if got.PeerAddr == "" {
			t.Errorf("the review for %s names no peer address", user)
		}
		if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		buf := make([]byte, len(ok))
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != ok {
			t.Fatalf("%s: response %q, %v; the released request did not come back", user, buf, err)
		}
	}
}
