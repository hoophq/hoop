package gate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

type detailPolicy struct{}

func (detailPolicy) Evaluate(inspect.Statement) policy.Verdict {
	v := policy.Deny("risky", "unbounded delete")
	v.Detail = "no WHERE clause"
	return v
}

func assertDetailOnlyOnTheDecision(t *testing.T, d gate.Decision, sink *recordingSink) {
	t.Helper()
	if d.Allowed || d.Message != "unbounded delete" || d.Detail != "no WHERE clause" {
		t.Errorf("Decision: Allowed %v, Message %q, Detail %q", d.Allowed, d.Message, d.Detail)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, ev := range sink.events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "WHERE clause") {
			t.Errorf("the audit trail carries the detail: %s", b)
		}
	}
}

func TestADenialDetailReachesTheDecisionAndNotTheTrail(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		sink := &recordingSink{}
		g, err := gate.New(newSession(), gate.Config{Protocol: inspect.Postgres, Policy: detailPolicy{}, Audit: sink})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		assertDetailOnlyOnTheDecision(t, g.Request(context.Background(), pgQuery("DELETE FROM customers")), sink)
	})
	t.Run("statement", func(t *testing.T) {
		sink := &recordingSink{}
		g, err := gate.NewStatementGate(newSession(), gate.Config{Policy: detailPolicy{}, Audit: sink})
		if err != nil {
			t.Fatalf("NewStatementGate: %v", err)
		}
		d := g.EvaluateStatement(context.Background(), inspect.Statement{
			Protocol: inspect.Postgres, Direction: inspect.FromClient,
			Text: "DELETE FROM customers", Operation: inspect.OpDelete,
		})
		assertDetailOnlyOnTheDecision(t, d, sink)
	})
}
