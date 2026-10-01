package session_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// The typed caller rides a context from the gate to the review filing, past
// two packages that never name a session. What goes in must come out whole,
// a context that never carried one must say so, and a nil context must not
// panic: the gate passes nil through for a caller that supplied none.
func TestIdentityRidesTheContext(t *testing.T) {
	want := session.Identity{
		Subject:  "alice@example.com",
		Email:    "alice@example.com",
		PeerAddr: "10.0.0.7:51234",
		Method:   session.MethodGoogleIdentity,
	}
	gone := errors.New("the client closed the connection")
	parent, cancel := context.WithCancelCause(context.Background())
	ctx := session.ContextWithIdentity(parent, want)

	got, ok := session.IdentityFromContext(ctx)
	if !ok {
		t.Fatal("the identity put on the context did not come back")
	}
	if got.Subject != want.Subject || got.Email != want.Email ||
		got.PeerAddr != want.PeerAddr || got.Method != want.Method {
		t.Errorf("the context returned %+v, want %+v", got, want)
	}

	// A child, not a replacement: the hold reads the connection's end and
	// its cause through the same context.
	cancel(gone)
	if ctx.Err() == nil || !errors.Is(context.Cause(ctx), gone) {
		t.Errorf("the child lost its parent's end: err %v, cause %v", ctx.Err(), context.Cause(ctx))
	}

	if id, ok := session.IdentityFromContext(context.Background()); ok || id.Subject != "" {
		t.Errorf("a context with no identity returned %+v, %v", id, ok)
	}
	var none context.Context
	if id, ok := session.IdentityFromContext(none); ok || id.Subject != "" {
		t.Errorf("a nil context returned %+v, %v", id, ok)
	}
}

// Method describes trust without enforcing any, so it must not reach OPA or
// the audit trail: a Rego rule keying on it would treat a label as a check.
// Two identities that differ only in Method render the same input.context.
func TestPolicyContextLeavesOutTheMethod(t *testing.T) {
	base := session.Identity{
		Subject:    "alice",
		Email:      "alice@example.com",
		Groups:     []string{"eng"},
		PeerAddr:   "10.0.0.7:51234",
		Attributes: map[string]string{"department": "platform"},
	}
	withMethod := base
	withMethod.Method = session.MethodSSHCertificate

	render := func(id session.Identity) map[string]string {
		s := session.Session{ID: "s1", Identity: id, Protocol: inspect.Postgres, Connection: "appdb"}
		return s.PolicyContext()
	}
	plain, marked := render(base), render(withMethod)
	if !maps.Equal(plain, marked) {
		t.Errorf("the method changed input.context:\n without %v\n    with %v", plain, marked)
	}
	for k, v := range marked {
		if v == string(session.MethodSSHCertificate) {
			t.Errorf("input.context[%q] carries the method", k)
		}
	}
}
