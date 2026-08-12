package gate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
)

// echoOPA allows everything and records the input document it was handed.
func echoOPA(t *testing.T) (string, func() map[string]any) {
	t.Helper()
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		last = body.Input
		if _, err := w.Write([]byte(`{"result":{"allow":true}}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() map[string]any { return last }
}

func principalFrom(t *testing.T, in map[string]any) string {
	t.Helper()
	ctx, ok := in["context"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := ctx["principal"].(string)
	return s
}

// A Rego policy that cannot name the actor cannot express "block this unless
// it is the on-call engineer", which is the whole reason to move a decision
// into OPA. The session facts must reach input.context.
func TestSessionContextReachesOPA(t *testing.T) {
	url, input := echoOPA(t)
	g, err := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   &policy.OPAClient{URL: url},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer g.Close(context.Background())

	if d := g.Request(context.Background(), pgQuery("SELECT 1")); d.Err != nil {
		t.Fatalf("Request: %v", d.Err)
	}
	if got := principalFrom(t, input()); got != "alice@example.com" {
		t.Errorf("input.context.principal = %q, want the session's actor", got)
	}
}

// The regression this exists for. The gate used to stamp session facts by
// copying the policy when it was a bare *policy.OPAClient, and a Chain is not
// one. Every sidecar lane builds a Chain, so input.context arrived empty on
// all of them, and a two-phase lane holds TWO clients inside that Chain.
func TestSessionContextReachesEveryOPAInAChain(t *testing.T) {
	url, input := echoOPA(t)
	g, err := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy: policy.Chain{
			denyDrops(t),
			&policy.OPAClient{URL: url, Phase: policy.PhaseGate},
			&policy.OPAClient{URL: url, Phase: policy.PhaseDecide},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer g.Close(context.Background())

	if d := g.Request(context.Background(), pgQuery("SELECT 1")); d.Err != nil {
		t.Fatalf("Request: %v", d.Err)
	}

	in := input()
	if got := principalFrom(t, in); got != "alice@example.com" {
		t.Errorf("input.context.principal = %q, want the session's actor", got)
	}
	// The recorded call is the last one, so this also pins that the decide
	// phase — not only the gate — carries the actor.
	if got, _ := in["phase"].(string); got != string(policy.PhaseDecide) {
		t.Errorf("last call was phase %q, want decide", got)
	}
}

// One client serves every connection on a lane, so a session's facts must not
// outlive the statement that carried them.
func TestSessionContextDoesNotLeakBetweenSessions(t *testing.T) {
	url, input := echoOPA(t)
	shared := &policy.OPAClient{URL: url}

	first, err := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres, Policy: policy.Chain{shared},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer first.Close(context.Background())
	if d := first.Request(context.Background(), pgQuery("SELECT 1")); d.Err != nil {
		t.Fatalf("Request: %v", d.Err)
	}

	other := newSession()
	other.Identity.Subject = "bob@example.com"
	second, err := gate.New(other, gate.Config{
		Protocol: hoopinspect.Postgres, Policy: policy.Chain{shared},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer second.Close(context.Background())
	if d := second.Request(context.Background(), pgQuery("SELECT 2")); d.Err != nil {
		t.Fatalf("Request: %v", d.Err)
	}

	if got := principalFrom(t, input()); got != "bob@example.com" {
		t.Errorf("input.context.principal = %q; one session's actor reached another's decision", got)
	}
	if shared.Context != nil {
		t.Errorf("the shared client retained a session's context: %v", shared.Context)
	}
}
