package gate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// ctxPolicy records the connection context the gate handed it, and denies
// with its cause the way a hold that saw the connection end does.
type ctxPolicy struct{ seen context.Context }

func (p *ctxPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	return p.EvaluateWith(stmt, nil)
}

func (p *ctxPolicy) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if ec != nil {
		p.seen = ec.ConnCtx
	}
	if p.seen != nil && p.seen.Err() != nil {
		return policy.Deny("hold", context.Cause(p.seen).Error())
	}
	return policy.Allow()
}

// cancelHonoringSink refuses a write under an ended context, as the SQLite
// store does.
type cancelHonoringSink struct{ recordingSink }

func (s *cancelHonoringSink) Write(ctx context.Context, ev audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.recordingSink.Write(ctx, ev)
}

// The caller's context reaches the evaluator, which is how a hold learns its
// connection ended, and the statement's record still lands under that ended
// context: it is written after the verdict, so the connection ending is
// exactly when it is written.
func TestTheConnectionContextReachesPolicyAndTheRecordSurvivesIt(t *testing.T) {
	pol := &ctxPolicy{}
	sink := &cancelHonoringSink{}
	g, err := gate.NewStatementGate(newSession(), gate.Config{Policy: pol, Audit: sink})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}

	gone := errors.New("the client closed the connection")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(gone)

	d := g.EvaluateStatement(ctx, inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
	})

	if pol.seen != ctx {
		t.Fatal("the evaluator did not receive the caller's context")
	}
	if d.Allowed {
		t.Fatal("a statement whose connection ended was allowed")
	}
	ev := sink.find(audit.KindViolation)
	if ev == nil {
		t.Fatal("the record was dropped because the connection had ended")
	}
	if ev.Message != gone.Error() {
		t.Errorf("the record says %q, want the cause %q", ev.Message, gone.Error())
	}
}
