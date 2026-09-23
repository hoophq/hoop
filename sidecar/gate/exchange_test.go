package gate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// optOutOPA allows everything and answers a request statement with
// `responses: false` when its text carries the marker. It records every
// direction it was asked about, in order.
func optOutOPA(t *testing.T, marker string) (string, func() []string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input struct {
				Direction string `json:"direction"`
				Statement string `json:"statement"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		seen = append(seen, body.Input.Direction)
		result := `{"result":{"allow":true}}`
		if body.Input.Direction == string(inspect.FromClient) && body.Input.Statement == marker {
			result = `{"result":{"allow":true,"responses":false}}`
		}
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string { return seen }
}

func grpcStmt(dir inspect.Direction, text string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.GRPC,
		Direction: dir,
		Text:      text,
		Operation: inspect.OpCall,
	}
}

// The policy's `responses: false` on a request must keep OPA off every
// response of that exchange, and off nothing else: the next exchange asks
// again. Both OPA clients of a two-phase chain are covered, because the
// veto is keyed by source, not by client.
func TestPolicyOptOutSkipsOPAOnTheExchangeResponses(t *testing.T) {
	url, seen := optOutOPA(t, "/bq.Storage/ReadRows")
	g, err := gate.NewStatementGate(
		session.New(inspect.GRPC, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol: inspect.GRPC,
			Policy: policy.Chain{
				&policy.OPAClient{URL: url, Phase: policy.PhaseGate},
				&policy.OPAClient{URL: url, Phase: policy.PhaseDecide},
			},
		})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	ctx := context.Background()

	// Exchange 1: the policy opts out of responses on the request.
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/bq.Storage/ReadRows"))
	if n := len(seen()); n != 2 {
		t.Fatalf("request cost %d OPA calls, want 2 (gate + decide)", n)
	}
	for i := range 3 {
		d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromServer, "row"))
		if !d.Allowed {
			t.Fatalf("row %d denied: %+v", i, d)
		}
	}
	if n := len(seen()); n != 2 {
		t.Fatalf("3 response messages cost %d extra OPA calls after the policy opted out", n-2)
	}

	// Exchange 2: a request the policy did not opt out of; its responses
	// are evaluated again.
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/bq.Storage/CreateSession"))
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromServer, "session"))
	got := seen()
	if len(got) != 6 || got[4] != string(inspect.FromServer) || got[5] != string(inspect.FromServer) {
		t.Errorf("calls = %v; the opt-out leaked into the next exchange", got)
	}
}

// The skipped rows still reach the audit trail, marked as never seen by
// OPA, so a row that says "allowed, no rule" is not mistaken for one OPA
// allowed.
func TestPolicyOptOutIsVisibleOnTheAuditRecord(t *testing.T) {
	url, _ := optOutOPA(t, "req")
	sink := &recordingSink{}
	g, err := gate.NewStatementGate(
		session.New(inspect.GRPC, session.Identity{Subject: "alice"}),
		gate.Config{Protocol: inspect.GRPC, Policy: policy.Chain{&policy.OPAClient{URL: url}}, Audit: sink})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	ctx := context.Background()
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "req"))
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromServer, "row"))

	sink.mu.Lock()
	events := sink.events
	sink.mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("%d audit events, want 2", len(events))
	}
	if _, marked := events[0].Metadata[policy.AnnotationOPASkipped]; marked {
		t.Error("the request, which OPA saw, was marked skipped")
	}
	if got := events[1].Metadata[policy.AnnotationOPASkipped]; got != "responses" {
		t.Errorf("response record opa.skipped = %q, want %q", got, "responses")
	}
}
