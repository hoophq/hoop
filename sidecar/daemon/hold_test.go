package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// holdPolicy stands in for the analyzer's hold on the endpoint lanes: it
// blocks every client statement carrying a payload (the ones a hold can
// fire on) until the test sends the verdict a reviewer would, or until the
// connection's context ends.
type holdPolicy struct {
	started chan struct{}
	verdict chan policy.Verdict
	ended   chan error
}

func newHoldPolicy() *holdPolicy {
	return &holdPolicy{
		started: make(chan struct{}, 1),
		verdict: make(chan policy.Verdict),
		ended:   make(chan error, 1),
	}
}

func (p *holdPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	return p.EvaluateWith(stmt, nil)
}

func (p *holdPolicy) EvaluateWith(stmt inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	held := stmt.Direction == inspect.FromClient &&
		(stmt.Operation == inspect.OpExecLine || (stmt.HTTP != nil && stmt.HTTP.Body != ""))
	if !held {
		return policy.Allow()
	}
	if ec == nil || ec.ConnCtx == nil {
		return policy.Deny("hold", "no connection context reached the policy")
	}
	p.started <- struct{}{}
	select {
	case v := <-p.verdict:
		return v
	case <-ec.ConnCtx.Done():
		p.ended <- context.Cause(ec.ConnCtx)
		return policy.Deny("hold", "the connection ended while waiting")
	}
}

func (p *holdPolicy) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement never reached the hold")
	}
}

// firstReadUpstream is a grpc upstream that reports the first request bytes
// it reads, so a test can tell "held" from "forwarded".
func firstReadUpstream(t *testing.T) (addr string, firstRead <-chan int) {
	t.Helper()
	reads := make(chan int, 1)
	addr, stop := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := io.ReadAtLeast(r.Body, buf, 1)
		reads <- n
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	t.Cleanup(stop)
	return addr, reads
}

// A held grpc or spanner message reaches the upstream only once released,
// and a refusal reaches the caller as PERMISSION_DENIED.
func TestAGRPCHoldForwardsOnlyWhatIsReleased(t *testing.T) {
	lanes := []struct {
		protocol, path string
		descriptors    func(*testing.T) string
		message        []byte
	}{
		{"grpc", "/test.v1.Echo/Say", writeGRPCTestDescriptors, marshalGRPCTestMessage("s", "v")},
		{"spanner", "/google.spanner.v1.Spanner/ExecuteSql", writeSpannerTestDescriptors,
			marshalSpannerSQLRequest("DELETE FROM accounts WHERE id = 7")},
	}
	verdicts := []struct {
		name    string
		verdict policy.Verdict
		status  string
		forward bool
	}{
		{name: "approved", verdict: policy.Allow(), status: "0", forward: true},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected"), status: "7"},
	}
	for _, ln := range lanes {
		for _, tc := range verdicts {
			t.Run(ln.protocol+"/"+tc.name, func(t *testing.T) {
				upstream, firstRead := firstReadUpstream(t)
				pol := newHoldPolicy()
				laneAddr, stop := startGRPCTestServer(t,
					buildHoldTestServer(t, ln.protocol, upstream, ln.descriptors(t), pol))
				defer stop()

				type result struct {
					resp *http.Response
					err  error
				}
				done := make(chan result, 1)
				go func() {
					resp, err := holdTestRoundTrip(context.Background(), laneAddr, ln.path, ln.message)
					done <- result{resp, err}
				}()
				pol.waitStarted(t)
				select {
				case n := <-firstRead:
					t.Fatalf("upstream read %d bytes while the message was held", n)
				case <-time.After(200 * time.Millisecond):
				}
				pol.verdict <- tc.verdict

				r := <-done
				if r.err != nil {
					t.Fatalf("round trip: %v", r.err)
				}
				if got := spannerTestStatus(r.resp); got != tc.status {
					t.Errorf("grpc-status = %q, want %q", got, tc.status)
				}
				var forwarded bool
				select {
				case n := <-firstRead:
					forwarded = n > 0
				case <-time.After(time.Second):
				}
				if forwarded != tc.forward {
					t.Errorf("upstream received the message: %v, want %v", forwarded, tc.forward)
				}
			})
		}
	}
}

// The caller's deadline is the budget on a grpc lane: when it passes, the
// client resets the stream and the hold stops waiting, before anything is
// claimed.
func TestAGRPCHoldEndsAtTheCallersDeadline(t *testing.T) {
	upstream, _ := firstReadUpstream(t)
	pol := newHoldPolicy()
	laneAddr, stop := startGRPCTestServer(t,
		buildHoldTestServer(t, "grpc", upstream, writeGRPCTestDescriptors(t), pol))
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() {
		resp, err := holdTestRoundTrip(ctx, laneAddr, "/test.v1.Echo/Say", marshalGRPCTestMessage("s", "v"))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	pol.waitStarted(t)
	select {
	case <-pol.ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the hold outlived the caller's deadline")
	}
}

// An ssh exec is judged before libhoop spawns anything: the callback does
// not return until the reviewer answers, and a rejection refuses the
// command with the hold's message.
func TestAnSSHExecHoldReturnsOnlyOnTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		refused bool
	}{
		{name: "approved", verdict: policy.Allow()},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected"), refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := newHoldPolicy()
			c := holdTestSSHConn(t, pol)

			done := make(chan error, 1)
			go func() {
				if r := c.exec(context.Background(), "rm -rf /srv/data"); r != nil {
					done <- errors.New(r.String())
					return
				}
				done <- nil
			}()
			pol.waitStarted(t)
			select {
			case <-done:
				t.Fatal("exec returned while the command was held")
			case <-time.After(200 * time.Millisecond):
			}
			pol.verdict <- tc.verdict

			select {
			case err := <-done:
				if refused := err != nil; refused != tc.refused {
					t.Errorf("refused = %v (%v), want %v", refused, err, tc.refused)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("exec never returned after the verdict")
			}
		})
	}
}

func buildHoldTestServer(t *testing.T, protocol, upstream, descriptors string, pol policy.Evaluator) GRPCServer {
	t.Helper()
	server, err := buildGRPCServer(lane{
		cfg: ListenerConfig{
			Name:     "hold-" + protocol,
			Protocol: protocol,
			Listen:   "127.0.0.1:0",
			Upstream: upstream,
			GRPC:     &GRPCCodecConfig{Descriptors: DescriptorPaths{descriptors}, CapturePayload: true},
		},
		name:   "hold-" + protocol,
		policy: pol,
	}, AuditConfig{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func holdTestRoundTrip(ctx context.Context, laneAddr, path string, message []byte) (*http.Response, error) {
	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+laneAddr+path,
		bytes.NewReader(grpcTestFrame(0, message)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp, nil
}

func holdTestSSHConn(t *testing.T, pol policy.Evaluator) *sshConnState {
	t.Helper()
	sess := session.New(inspect.SSH, session.Identity{Subject: "alice@example.com"})
	sess.Connection = "prod-endpoint"
	g, err := gate.NewStatementGate(sess, gate.Config{
		Protocol: inspect.SSH,
		Policy:   pol,
		Audit:    &sshTestSink{},
	})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return &sshConnState{
		gate:  g,
		stmts: sshStatements{},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
