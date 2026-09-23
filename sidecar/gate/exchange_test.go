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
// response of that exchange. A statement gate IS one exchange (the gRPC
// lane builds one per RPC), so the opt-out lives as long as the gate, and a
// later request message that says nothing does not undo it. Both OPA
// clients of a two-phase chain are covered, because the veto is keyed by
// source, not by client.
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

	// The headers statement opts out; the request message that follows
	// (a client-streaming RPC) says nothing.
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "/bq.Storage/ReadRows"))
	g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "request message"))
	if n := len(seen()); n != 4 {
		t.Fatalf("two request statements cost %d OPA calls, want 4 (gate + decide each)", n)
	}
	for i := range 3 {
		d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromServer, "row"))
		if !d.Allowed {
			t.Fatalf("row %d denied: %+v", i, d)
		}
	}
	if n := len(seen()); n != 4 {
		t.Fatalf("3 response messages cost %d extra OPA calls after the policy opted out", n-4)
	}
}

// A connection gate serves many exchanges and cannot tell which response
// answers which request: HTTP/1.1 pipelining puts request B on the wire
// before response A. Honoring A's `responses: false` there would silence
// OPA on B's response, so the gate keeps asking and marks A's record.
func TestPolicyOptOutIsIgnoredOnAConnectionGate(t *testing.T) {
	url, seen := optOutOPA(t, "GET /a")
	sink := &recordingSink{}
	g, err := gate.New(
		session.New(inspect.HTTP, session.Identity{Subject: "alice"}),
		gate.Config{Protocol: inspect.HTTP, Policy: policy.Chain{&policy.OPAClient{URL: url}}, Audit: sink})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// A and B pipelined in one segment; only A opts out.
	pipelined := "GET /a HTTP/1.1\r\nHost: x\r\n\r\n" + "GET /b HTTP/1.1\r\nHost: x\r\n\r\n"
	if d := g.Request(ctx, []byte(pipelined)); !d.Allowed || len(d.Statements) != 2 {
		t.Fatalf("pipelined requests: %+v", d)
	}
	responses := "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n" + "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"
	if d := g.Response(ctx, []byte(responses)); !d.Allowed || len(d.Statements) != 2 {
		t.Fatalf("pipelined responses: %+v", d)
	}

	got := seen()
	want := []string{"client", "client", "server", "server"}
	if len(got) != len(want) {
		t.Fatalf("OPA calls = %v, want %v; B's response must still reach OPA", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OPA calls = %v, want %v", got, want)
		}
	}

	sink.mu.Lock()
	events := sink.events
	sink.mu.Unlock()
	var marked int
	for _, ev := range events {
		if ev.Metadata[policy.AnnotationResponsesUnscoped] == "connection" {
			marked++
			if ev.Statement != "GET /a" {
				t.Errorf("record %q marked unscoped; only A's request opted out", ev.Statement)
			}
		}
	}
	if marked != 1 {
		t.Errorf("%d records marked opa.responses_unscoped, want 1 (A's request)", marked)
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
