package session_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/session"
)

// Session ids appear in audit records that get correlated across systems. A
// guessable id lets an attacker probe for another user's trail.
func TestNewIDIsUniqueAndOpaque(t *testing.T) {
	seen := map[session.ID]bool{}
	for range 1000 {
		id := session.NewID()
		if seen[id] {
			t.Fatalf("duplicate session id %q in 1000 draws", id)
		}
		seen[id] = true
		if len(id) != 32 {
			t.Fatalf("id %q is %d chars, want 32 hex", id, len(id))
		}
	}
}

// The audit trail needs something in the actor column even when the identity
// provider gave nothing.
func TestPrincipalFallback(t *testing.T) {
	tests := []struct {
		name string
		id   session.Identity
		want string
	}{
		{"subject wins", session.Identity{Subject: "sub", Email: "e@x"}, "sub"},
		{"email fallback", session.Identity{Email: "e@x"}, "e@x"},
		{"anonymous", session.Identity{PeerAddr: "10.0.0.1:1"}, "anonymous"},
		{"empty", session.Identity{}, "anonymous"},
	}
	for _, tc := range tests {
		if got := tc.id.Principal(); got != tc.want {
			t.Errorf("%s: Principal = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsAnonymous(t *testing.T) {
	if !(session.Identity{PeerAddr: "1.2.3.4:5"}).IsAnonymous() {
		t.Error("an identity with only a peer address is not anonymous")
	}
	if (session.Identity{Subject: "s"}).IsAnonymous() {
		t.Error("an identity with a subject is anonymous")
	}
	if (session.Identity{Email: "e@x"}).IsAnonymous() {
		t.Error("an identity with an email is anonymous")
	}
}

// A double End in a defer chain must not move the timestamp, or the recorded
// duration silently grows.
func TestEndIsIdempotent(t *testing.T) {
	s := session.New(hoopinspect.Postgres, session.Identity{Subject: "a"})
	s.End()
	first := s.EndedAt

	time.Sleep(2 * time.Millisecond)
	s.End()

	if !s.EndedAt.Equal(first) {
		t.Error("a second End moved EndedAt; the duration would be wrong")
	}
}

func TestDurationBeforeAndAfterEnd(t *testing.T) {
	s := session.New(hoopinspect.Postgres, session.Identity{Subject: "a"})
	if !s.IsOpen() {
		t.Error("a new session is not open")
	}
	if s.Duration() <= 0 {
		t.Error("an open session reports a non-positive duration")
	}

	time.Sleep(2 * time.Millisecond)
	s.End()

	if s.IsOpen() {
		t.Error("session still open after End")
	}
	d := s.Duration()
	if d <= 0 {
		t.Errorf("Duration = %v after End", d)
	}
	// Duration must now be frozen.
	time.Sleep(2 * time.Millisecond)
	if s.Duration() != d {
		t.Error("Duration kept growing after End")
	}
}

// PolicyContext is the input.context a Rego rule reads. Field names are a
// public contract: renaming one silently breaks someone's policy.
func TestPolicyContextShape(t *testing.T) {
	s := session.New(hoopinspect.Postgres, session.Identity{
		Subject:    "alice@example.com",
		Email:      "alice@example.com",
		Groups:     []string{"eng", "oncall"},
		PeerAddr:   "10.0.0.7:51234",
		Attributes: map[string]string{"department": "platform"},
	})
	s.Connection = "appdb"
	s.Upstream = "db.internal:5432"
	s.CorrelationID = "ticket-4711"
	s.Metadata = map[string]string{"region": "us-east-1"}

	ctx := s.PolicyContext()

	for key, want := range map[string]string{
		"principal":      "alice@example.com",
		"subject":        "alice@example.com",
		"email":          "alice@example.com",
		"peer_addr":      "10.0.0.7:51234",
		"connection":     "appdb",
		"upstream":       "db.internal:5432",
		"correlation_id": "ticket-4711",
		"groups":         "eng,oncall",
		"department":     "platform",
		"region":         "us-east-1",
	} {
		if ctx[key] != want {
			t.Errorf("context[%q] = %q, want %q", key, ctx[key], want)
		}
	}
	if ctx["session_id"] != string(s.ID) {
		t.Errorf("context[session_id] = %q, want %q", ctx["session_id"], s.ID)
	}
}

// Absent facts must be omitted rather than present as empty strings: a Rego
// rule written as `input.context.connection == ""` should not match every
// session that never set one.
func TestPolicyContextOmitsEmptyFields(t *testing.T) {
	s := session.New(hoopinspect.Postgres, session.Identity{Subject: "a"})
	ctx := s.PolicyContext()

	for _, key := range []string{"email", "peer_addr", "connection", "upstream", "correlation_id", "groups"} {
		if _, present := ctx[key]; present {
			t.Errorf("context[%q] present though unset", key)
		}
	}
	if ctx["principal"] != "a" {
		t.Errorf("principal = %q", ctx["principal"])
	}
}

// Metadata overrides nothing structural and must still reach the policy. A
// deployment-specific fact is the reason the field exists.
func TestPolicyContextIncludesMetadata(t *testing.T) {
	s := session.New(hoopinspect.HTTP, session.Identity{Subject: "a"})
	s.Metadata = map[string]string{"tenant": "acme"}

	if got := s.PolicyContext()["tenant"]; got != "acme" {
		t.Errorf("context[tenant] = %q, want acme", got)
	}
}

func TestNewSetsFields(t *testing.T) {
	before := time.Now().UTC()
	s := session.New(hoopinspect.HTTP, session.Identity{Subject: "bob"})

	if s.Protocol != hoopinspect.HTTP {
		t.Errorf("Protocol = %q", s.Protocol)
	}
	if s.ID == "" {
		t.Error("ID not set")
	}
	if s.StartedAt.Before(before) {
		t.Error("StartedAt precedes the call")
	}
	if s.StartedAt.Location() != time.UTC {
		t.Error("StartedAt is not UTC; audit timestamps must not be local")
	}
	if !s.EndedAt.IsZero() {
		t.Error("EndedAt set on a new session")
	}
}

func TestGroupsJoinedWithoutTrailingSeparator(t *testing.T) {
	s := session.New(hoopinspect.Postgres, session.Identity{
		Subject: "a", Groups: []string{"one"},
	})
	if got := s.PolicyContext()["groups"]; got != "one" {
		t.Errorf("groups = %q, want %q", got, "one")
	}

	s2 := session.New(hoopinspect.Postgres, session.Identity{
		Subject: "a", Groups: []string{"one", "two", "three"},
	})
	got := s2.PolicyContext()["groups"]
	if got != "one,two,three" {
		t.Errorf("groups = %q", got)
	}
	if strings.HasSuffix(got, ",") {
		t.Error("groups has a trailing separator")
	}
}
