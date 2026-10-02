package gate_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// highRiskProvider rates every statement high, so an analyzer that maps high
// to require_review holds everything it is shown.
type highRiskProvider struct{}

func (highRiskProvider) Name() string { return "stub" }

func (highRiskProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh}, nil
}

// filingReviewer leaves every review PENDING and keeps the caller each
// filing's context carried, which is what the daemon sends as the requester.
type filingReviewer struct {
	mu      sync.Mutex
	callers []session.Identity
	named   []bool
}

func (r *filingReviewer) File(ctx context.Context, _ string) (analyzer.ReviewResult, error) {
	id, ok := session.IdentityFromContext(ctx)
	r.mu.Lock()
	r.callers = append(r.callers, id)
	r.named = append(r.named, ok)
	r.mu.Unlock()
	return analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}, nil
}

func (*filingReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{ID: id, Status: "PENDING"}, nil
}

// The review filing names who filed from the TYPED caller, not from
// input.context. input.context merges Attributes over its own keys, so an
// attribute spelled `email` renames the caller there, as it always has; it
// must not rename the filer an approver reads.
//
// End to end through a policy.Chain and the analyzer's hold, because that is
// the path the caller travels: the gate puts it on ConnCtx, the chain hands
// the same EvalContext on, and the hold's ask keeps the values while it drops
// the cancellation. The client's review mode rides the same context, and the
// wrapper must keep it: a return-mode denial proves it did, since the
// listener's own mode here is hold.
func TestTheChainSeesTheTypedCaller(t *testing.T) {
	rec := &contextRecorder{}
	rev := &filingReviewer{}
	ev, err := analyzer.New(analyzer.Config{
		Rule:     "payments",
		Provider: highRiskProvider{},
		Trigger:  analyzer.Trigger{Operations: []inspect.Operation{inspect.OpDelete}},
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		Review:   rev,
	})
	if err != nil {
		t.Fatalf("analyzer.New: %v", err)
	}
	sess := session.New(inspect.Postgres, session.Identity{
		Subject:    "alice",
		Email:      "alice@example.com",
		PeerAddr:   "10.0.0.7:51234",
		Method:     session.MethodSSHCertificate,
		Attributes: map[string]string{"email": "mallory@x"},
	})
	g, err := gate.NewStatementGate(sess, gate.Config{Policy: policy.Chain{rec, ev}})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}

	// Bounded, so a lost review mode fails on the message below instead of
	// holding the test for the whole review budget.
	base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := analyzer.WithClientReviewMode(base, analyzer.ReviewReturn)
	d := g.EvaluateStatement(ctx, inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
		Tables:    []string{"users"},
	})

	if d.Allowed {
		t.Fatal("a held statement was allowed")
	}
	if !strings.Contains(d.Message, "resend the identical statement") {
		t.Errorf("denial %q is not a return-mode one: the client's review mode did not survive", d.Message)
	}
	if len(rec.seen) != 1 || rec.seen[0]["email"] != "mallory@x" {
		t.Errorf("input.context = %v, want email still mallory@x as before", rec.seen)
	}
	if len(rec.callers) != 1 || !rec.named[0] || rec.callers[0].Email != "alice@example.com" {
		t.Errorf("the chain saw the typed caller %+v (present %v), want alice@example.com", rec.callers, rec.named)
	}
	rev.mu.Lock()
	defer rev.mu.Unlock()
	if len(rev.callers) != 1 || !rev.named[0] {
		t.Fatalf("the reviewer saw %d filings, typed caller present %v", len(rev.callers), rev.named)
	}
	got := rev.callers[0]
	if got.Subject != "alice" || got.Email != "alice@example.com" ||
		got.PeerAddr != "10.0.0.7:51234" || got.Method != session.MethodSSHCertificate {
		t.Errorf("the filing named %+v, want alice, alice@example.com, the peer and ssh_certificate", got)
	}
}

// Envoy pools one upstream connection across users, and a review filed on it
// must name the caller of THAT request. The caller is attached per statement,
// after the identity check rotated the session, so bob's request after
// alice's carries bob.
func TestARotatedCallerRidesTheContext(t *testing.T) {
	ctx := context.Background()
	rec := &contextRecorder{}
	g := identityGate(t, &recordingSink{}, rec, "alice@example.com")

	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))
	mustAllow(t, g.Request(ctx, []byte("REQ tok-bob\n")))
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	want := []string{"alice@example.com", "alice@example.com", "bob@example.com", "bob@example.com"}
	if len(rec.callers) != len(want) {
		t.Fatalf("the policy judged %d statements, want %d", len(rec.callers), len(want))
	}
	for i, w := range want {
		if !rec.named[i] || rec.callers[i].Subject != w {
			t.Errorf("statement %d carried caller %q (present %v), want %q", i, rec.callers[i].Subject, rec.named[i], w)
		}
		if got := rec.seen[i]["principal"]; got != w {
			t.Errorf("statement %d: input.context names %q and the typed caller %q", i, got, rec.callers[i].Subject)
		}
	}
}
