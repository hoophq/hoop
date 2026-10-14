package gate_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// reservingPolicy releases a request whose text holds "/held/" on a reserved
// approval named after the text, refuses one holding "/deny", and allows the
// rest: what the analyzer answers on a lane that settles.
type reservingPolicy struct{}

func (reservingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	if stmt.Direction != inspect.FromClient {
		return policy.Allow()
	}
	switch {
	case strings.Contains(stmt.Text, "/deny"):
		return policy.Deny("rule", "refused")
	case strings.Contains(stmt.Text, "/held/"):
		_, id, _ := strings.Cut(stmt.Text, "/held/")
		id, _, _ = strings.Cut(id, " ")
		return policy.Verdict{Review: &policy.Review{ID: id, Status: "PROCESSING", Reserved: true}}
	}
	return policy.Allow()
}

// settleLog records every settle as "<id>=<accepted>", in order.
type settleLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *settleLog) settle(_ context.Context, id string, accepted bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("%s=%v", id, accepted))
}

func (l *settleLog) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

func settlingHTTPGate(t *testing.T) (*gate.Gate, *settleLog) {
	t.Helper()
	log := &settleLog{}
	g, err := gate.New(session.New(inspect.HTTP, session.Identity{}), gate.Config{
		Protocol: inspect.HTTP,
		Policy:   reservingPolicy{},
		Settle:   log.settle,
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	return g, log
}

func request(path string) string {
	return "POST " + path + " HTTP/1.1\r\nHost: h\r\nContent-Length: 0\r\n\r\n"
}

func response(status string) string {
	return "HTTP/1.1 " + status + "\r\nContent-Length: 0\r\n\r\n"
}

// The settle runs inside Response, so a client that resends after reading
// the refusal finds the approval back.
func TestAnUpstreamAnswerSettlesTheReservedApproval(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"401 Unauthorized", "a=false"},
		{"403 Forbidden", "a=false"},
		{"409 Conflict", "a=false"},
		{"200 OK", "a=true"},
		{"302 Found", "a=true"},
		{"500 Internal Server Error", "a=true"},
		{"504 Gateway Timeout", "a=true"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			g, log := settlingHTTPGate(t)
			ctx := context.Background()
			if d := g.Request(ctx, []byte(request("/held/a"))); !d.Allowed {
				t.Fatalf("the reserved request was refused: %s", d.Message)
			}
			if got := log.got(); len(got) != 0 {
				t.Fatalf("settled before the upstream answered: %v", got)
			}
			if d := g.Response(ctx, []byte(response(tc.status))); !d.Allowed {
				t.Fatalf("the response was refused: %s", d.Message)
			}
			if got := log.got(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("settles = %v, want [%s]", got, tc.want)
			}
		})
	}
}

// HTTP/1.1 answers in request order: each response settles its own request,
// and a request that reserved nothing settles nothing.
func TestEachResponseSettlesItsOwnRequest(t *testing.T) {
	g, log := settlingHTTPGate(t)
	ctx := context.Background()
	g.Request(ctx, []byte(request("/plain")+request("/held/b")))
	g.Response(ctx, []byte(response("100 Continue")))
	if got := log.got(); len(got) != 0 {
		t.Fatalf("an interim response settled %v", got)
	}
	g.Response(ctx, []byte(response("200 OK")+response("401 Unauthorized")))
	if got := log.got(); !slices.Equal(got, []string{"b=false"}) {
		t.Errorf("settles = %v, want [b=false]", got)
	}
}

// A chunk refused after a reserved request in it forwards nothing, so the
// reserved request never ran.
func TestARefusedChunkGivesBackTheApprovalsItReserved(t *testing.T) {
	g, log := settlingHTTPGate(t)
	if d := g.Request(context.Background(), []byte(request("/held/c")+request("/deny"))); d.Allowed {
		t.Fatal("the chunk was allowed")
	}
	if got := log.got(); !slices.Equal(got, []string{"c=false"}) {
		t.Errorf("settles = %v, want [c=false]", got)
	}
}

// With no answer the upstream may still have run the statement.
func TestAConnectionThatEndsUnansweredSpendsTheApproval(t *testing.T) {
	g, log := settlingHTTPGate(t)
	ctx := context.Background()
	g.Request(ctx, []byte(request("/held/d")))
	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := log.got(); !slices.Equal(got, []string{"d=true"}) {
		t.Errorf("settles = %v, want [d=true]", got)
	}
}

func TestAGateWithoutSettleForwardsAReservedStatement(t *testing.T) {
	g, err := gate.New(session.New(inspect.HTTP, session.Identity{}), gate.Config{
		Protocol: inspect.HTTP,
		Policy:   reservingPolicy{},
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	ctx := context.Background()
	if d := g.Request(ctx, []byte(request("/held/e"))); !d.Allowed {
		t.Fatalf("refused: %s", d.Message)
	}
	if d := g.Response(ctx, []byte(response("401 Unauthorized"))); !d.Allowed {
		t.Fatalf("refused: %s", d.Message)
	}
	_ = g.Close(ctx)
}

// A gRPC call is one statement gate. A response message means the call is
// running; the trailer's grpc-status decides a call that sent none.
func TestAGRPCAnswerSettlesTheCallsReservations(t *testing.T) {
	trailer := func(code string) inspect.Statement {
		s := grpcStmt(inspect.FromServer, "/svc/M")
		s.Metadata = map[string]string{inspect.MetadataGRPCStatusCode: code}
		return s
	}
	cases := []struct {
		name   string
		answer inspect.Statement
		want   string
	}{
		{"unauthenticated", trailer("16"), "f=false"},
		{"permission_denied", trailer("7"), "f=false"},
		{"invalid_argument", trailer("3"), "f=false"},
		{"ok", trailer("0"), "f=true"},
		{"unavailable", trailer("14"), "f=true"},
		{"cancelled", trailer("1"), "f=true"},
		{"internal", trailer("13"), "f=true"},
		{"response message", grpcStmt(inspect.FromServer, "{}"), "f=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &settleLog{}
			g, err := gate.NewStatementGate(session.New(inspect.GRPC, session.Identity{}), gate.Config{
				Protocol: inspect.GRPC,
				Policy:   reservingPolicy{},
				Settle:   log.settle,
			})
			if err != nil {
				t.Fatalf("NewStatementGate: %v", err)
			}
			ctx := context.Background()
			if d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/held/f")); !d.Allowed {
				t.Fatalf("refused: %s", d.Message)
			}
			g.EvaluateStatement(ctx, tc.answer)
			// A later trailer finds nothing left to settle.
			g.EvaluateStatement(ctx, trailer("16"))
			_ = g.Close(ctx)
			if got := log.got(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("settles = %v, want [%s]", got, tc.want)
			}
		})
	}
}

// A fail-closed audit refusal forwards nothing, so the approval the policy
// reserved for the statement goes back at once.
func TestAnAuditRefusalGivesBackTheReservation(t *testing.T) {
	for name, statementGate := range map[string]bool{"http": false, "grpc": true} {
		t.Run(name, func(t *testing.T) {
			log := &settleLog{}
			cfg := gate.Config{
				Policy:           reservingPolicy{},
				Audit:            &recordingSink{failOn: audit.KindStatement},
				FailOnAuditError: true,
				Settle:           log.settle,
			}
			ctx := context.Background()
			var allowed bool
			if statementGate {
				cfg.Protocol = inspect.GRPC
				g, err := gate.NewStatementGate(session.New(inspect.GRPC, session.Identity{}), cfg)
				if err != nil {
					t.Fatalf("NewStatementGate: %v", err)
				}
				allowed = g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/held/g")).Allowed
			} else {
				cfg.Protocol = inspect.HTTP
				g, err := gate.New(session.New(inspect.HTTP, session.Identity{}), cfg)
				if err != nil {
					t.Fatalf("gate.New: %v", err)
				}
				allowed = g.Request(ctx, []byte(request("/held/g"))).Allowed
			}
			if allowed {
				t.Fatal("the audit failure did not refuse the statement")
			}
			if got := log.got(); !slices.Equal(got, []string{"g=false"}) {
				t.Errorf("settles = %v, want [g=false]", got)
			}
		})
	}
}

// A refused later request ends a gRPC call with the sidecar's own status, so
// the reservations already forwarded are spent, not given back by it.
func TestALaneRefusalSpendsTheCallsReservations(t *testing.T) {
	log := &settleLog{}
	g, err := gate.NewStatementGate(session.New(inspect.GRPC, session.Identity{}), gate.Config{
		Protocol: inspect.GRPC,
		Policy:   reservingPolicy{},
		Settle:   log.settle,
	})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	ctx := context.Background()
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/held/h"))
	if d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/deny")); d.Allowed {
		t.Fatal("the refused request was allowed")
	}
	trailer := grpcStmt(inspect.FromServer, "/svc/M")
	trailer.Metadata = map[string]string{inspect.MetadataGRPCStatusCode: "7"}
	g.EvaluateStatement(ctx, trailer)
	_ = g.Close(ctx)
	if got := log.got(); !slices.Equal(got, []string{"h=true"}) {
		t.Errorf("settles = %v, want [h=true]", got)
	}
}
