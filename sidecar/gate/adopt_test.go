package gate_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// contextRecorder allows everything and keeps the input.context each
// statement was judged under.
type contextRecorder struct {
	mu   sync.Mutex
	seen []map[string]string
}

func (r *contextRecorder) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

func (r *contextRecorder) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, maps.Clone(ec.Context))
	return policy.Allow()
}

// A pgwire lane learns its user and startup metadata after gate.New has
// built the policy context. Adopt is what puts them in front of OPA; without
// it a Rego rule reads `anonymous` for a session the audit trail attributes
// to alice.
func TestAdoptReachesThePolicyContext(t *testing.T) {
	rec := &contextRecorder{}
	sess := session.New(inspect.Postgres, session.Identity{PeerAddr: "10.0.0.7:51234"})
	g, err := gate.New(sess, gate.Config{Protocol: inspect.Postgres, Policy: rec})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.Adopt("alice", map[string]string{"claude.session.id": "xyz1234678"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	ctx := context.Background()
	g.Request(ctx, pgQuery("SELECT 1"))

	if len(rec.seen) != 1 {
		t.Fatalf("policy judged %d statements, want 1", len(rec.seen))
	}
	if got := rec.seen[0]["principal"]; got != "alice" {
		t.Errorf("input.context.principal = %q, want alice", got)
	}
	if got := rec.seen[0]["claude.session.id"]; got != "xyz1234678" {
		t.Errorf("input.context[claude.session.id] = %q, want xyz1234678", got)
	}

	// Two statements of one session judged against two different contexts
	// could not be explained afterwards, so the facts freeze at the first.
	if err := g.Adopt("mallory", nil); !errors.Is(err, gate.ErrSessionStarted) {
		t.Fatalf("Adopt after a statement = %v, want ErrSessionStarted", err)
	}
	if got := g.Session().Identity.Subject; got != "alice" {
		t.Errorf("subject = %q after a refused Adopt, want alice", got)
	}
}

// A subject an IdentityFn resolved came from something that verified it;
// the StartupMessage's user is a claim and must not replace it.
func TestAdoptKeepsAResolvedSubject(t *testing.T) {
	sess := session.New(inspect.Postgres, session.Identity{Subject: "alice@example.com"})
	g, err := gate.New(sess, gate.Config{Protocol: inspect.Postgres})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.Adopt("postgres", nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := g.Session().Identity.Subject; got != "alice@example.com" {
		t.Errorf("subject = %q, want the resolved alice@example.com", got)
	}
}
