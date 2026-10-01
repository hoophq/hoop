package gate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
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

// connMarker is a value the caller puts on its context, to prove the
// evaluator's context descends from it.
type connMarker struct{}

// The caller's context reaches the evaluator, which is how a hold learns its
// connection ended, and the statement's record still lands under that ended
// context: it is written after the verdict, so the connection ending is
// exactly when it is written.
//
// The evaluator receives a CHILD of the caller's context, the one carrying
// the typed caller, so this checks what the child must keep: the caller's
// values, its end and its cause (the denial below carries the cause).
func TestTheConnectionContextReachesPolicyAndTheRecordSurvivesIt(t *testing.T) {
	pol := &ctxPolicy{}
	sink := &cancelHonoringSink{}
	g, err := gate.NewStatementGate(newSession(), gate.Config{Policy: pol, Audit: sink})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}

	gone := errors.New("the client closed the connection")
	ctx, cancel := context.WithCancelCause(context.WithValue(context.Background(), connMarker{}, "conn-1"))
	cancel(gone)

	d := g.EvaluateStatement(ctx, inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
	})

	if pol.seen == nil || pol.seen.Value(connMarker{}) != "conn-1" {
		t.Fatal("the evaluator did not receive the caller's context")
	}
	if !errors.Is(context.Cause(pol.seen), gone) {
		t.Errorf("the evaluator's context lost the cause: %v", context.Cause(pol.seen))
	}
	if id, ok := session.IdentityFromContext(pol.seen); !ok || id.Subject != "alice@example.com" {
		t.Errorf("the evaluator's context carried caller %+v (present %v), want the session's", id, ok)
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
