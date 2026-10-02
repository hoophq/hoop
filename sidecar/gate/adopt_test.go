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
// statement was judged under, and the typed caller its ConnCtx carried
// (named reports whether it carried one at all).
type contextRecorder struct {
	mu      sync.Mutex
	seen    []map[string]string
	callers []session.Identity
	named   []bool
}

func (r *contextRecorder) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

func (r *contextRecorder) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, maps.Clone(ec.Context))
	id, ok := session.IdentityFromContext(ec.ConnCtx)
	r.callers = append(r.callers, id)
	r.named = append(r.named, ok)
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
	// The claim filled nothing, so it must not relabel the resolved name as
	// a database login either.
	if got := g.Session().Identity.Method; got != "" {
		t.Errorf("method = %q after a refused claim, want the resolved identity's own (none)", got)
	}

	// A resolved identity that says how it was established keeps saying so.
	sess = session.New(inspect.Postgres, session.Identity{
		Subject: "alice@example.com", Method: session.MethodIdentityHeader,
	})
	g, err = gate.New(sess, gate.Config{Protocol: inspect.Postgres})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.Adopt("postgres", nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := g.Session().Identity.Method; got != session.MethodIdentityHeader {
		t.Errorf("method = %q, want the resolved identity_header", got)
	}
}

// The StartupMessage's user is a claim until the server answers
// AuthenticationOk, and a statement pipelined before that is judged, and can
// be held for review, under it. Adopt marks it database_user so a reviewer
// can see the name was claimed, not proved, and the statement's context
// names the same caller the session does.
func TestAdoptNamesTheDatabaseUser(t *testing.T) {
	rec := &contextRecorder{}
	sess := session.New(inspect.Postgres, session.Identity{PeerAddr: "10.0.0.7:51234"})
	g, err := gate.New(sess, gate.Config{Protocol: inspect.Postgres, Policy: rec})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.Adopt("alice", nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	id := g.Session().Identity
	if id.Subject != "alice" || id.Method != session.MethodDatabaseUser {
		t.Errorf("identity = %q/%q, want alice/%s", id.Subject, id.Method, session.MethodDatabaseUser)
	}

	g.Request(context.Background(), pgQuery("SELECT 1"))
	if len(rec.callers) != 1 || !rec.named[0] {
		t.Fatalf("the policy saw %d statements, typed caller present %v", len(rec.callers), rec.named)
	}
	if got := rec.callers[0]; got.Subject != "alice" || got.Method != session.MethodDatabaseUser ||
		got.PeerAddr != "10.0.0.7:51234" {
		t.Errorf("the statement's caller = %+v, want alice, database_user and the peer", got)
	}
}
