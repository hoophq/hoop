package license

import (
	"strings"
	"testing"
	"time"
)

// term signs a license with the boundary these tests need and puts it
// through the real Load, so the verdict under test is one a customer could
// hold. Nothing here can shortcut the signature: there is no constructor for
// that, which is the property the tests below lean on.
func term(t *testing.T, from, to time.Time) Status {
	t.Helper()
	key := signingKey(t)
	l := issue(t, key, Payload{
		Type:         EnterpriseType,
		IssuedAt:     from.Unix(),
		ExpireAt:     to.Unix(),
		AllowedHosts: []string{"*"},
		Description:  "Acme Corp",
	})
	return Load(Ref{Value: document(t, l), Source: "the test"})
}

// The bug this file exists for. A process that verified a license at startup
// used to hold the caps for its whole life, because the verdict was a field
// set once. Ask the same Status either side of its expiry and it has to
// answer differently.
func TestAVerdictExpiresWithoutBeingReloaded(t *testing.T) {
	start := time.Now().UTC()
	s := term(t, start.Add(-time.Hour), start.Add(time.Hour))

	if got := s.StateAt(start); got != StateValid {
		t.Fatalf("inside the term: state = %q", got)
	}
	if !s.AllowsAt(start, FeatureGuardrails) {
		t.Fatal("inside the term: the license granted nothing")
	}

	after := start.Add(2 * time.Hour)
	if got := s.StateAt(after); got != StateExpired {
		t.Errorf("past the term: state = %q, want expired", got)
	}
	if s.AllowsAt(after, FeatureGuardrails) || s.AllowsAt(after, FeatureDataMasking) {
		t.Error("past the term: the license still granted a feature")
	}
}

// The instant itself. ExpireAt is the last second the license covers, so a
// verdict taken exactly on it still grants and one a second later does not.
func TestTheBoundaryIsExpireAt(t *testing.T) {
	end := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	s := term(t, end.Add(-24*time.Hour), end)

	if !s.AllowsAt(end, FeatureGuardrails) {
		t.Error("the license stopped granting at the last second of its term")
	}
	if s.AllowsAt(end.Add(time.Second), FeatureGuardrails) {
		t.Error("the license granted a second past its term")
	}
}

// Allows reads the wall clock, so a caller that never touches StateAt still
// gets an aging verdict. This is what the daemon calls.
func TestAllowsReadsTheClock(t *testing.T) {
	now := time.Now().UTC()
	if live := term(t, now.Add(-time.Hour), now.Add(time.Hour)); !live.Allows(FeatureGuardrails) {
		t.Error("a current license granted nothing")
	}
	if dead := term(t, now.Add(-48*time.Hour), now.Add(-time.Hour)); dead.Allows(FeatureGuardrails) {
		t.Error("a license whose term ended an hour ago still granted a feature")
	}
}

// The operator has to see the transition, not just feel it. Both the startup
// line and the admin report are derived, so they move with the verdict.
func TestTheReportAndTheLineFollowTheVerdict(t *testing.T) {
	now := time.Now().UTC()
	s := term(t, now.Add(-48*time.Hour), now.Add(-time.Hour))

	if got := s.Report()["state"]; got != "expired" {
		t.Errorf("report state = %v, want expired", got)
	}
	if !strings.Contains(s.Line(), "expired") {
		t.Errorf("the line does not say expired: %q", s.Line())
	}
	// No Err: this license loaded clean and ran out later. The reason has
	// to come from the term instead, or /config reports a problem with no
	// explanation.
	if s.Err != nil {
		t.Fatalf("a license that expired after loading carries an Err: %v", s.Err)
	}
	for _, want := range []string{"expired on", Support} {
		if !strings.Contains(s.Reason(), want) {
			t.Errorf("Reason() is missing %q: %q", want, s.Reason())
		}
	}
	if got := s.Report()["problem"]; got == nil {
		t.Error("the report names no problem for an expired license")
	}
}

// A license that verified and then ran out grants nothing, whatever the
// verdict said when it loaded.
func TestALoadedLicenseCannotSkipTheTerm(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	s := term(t, past.Add(-24*time.Hour), past)

	if s.State() != StateExpired {
		t.Errorf("state = %q, want expired", s.State())
	}
	if s.Allows(FeatureDataMasking) {
		t.Error("a hand-built verdict granted a feature past its term")
	}
}

// A document that never verified stays invalid whatever the clock says. The
// expiry check must not become a way around a bad signature.
func TestAnUnverifiedStatusNeverBecomesValid(t *testing.T) {
	s := Load(Ref{Value: "/nonexistent/license.json", Source: "the test"})
	future := time.Now().UTC().Add(-999 * time.Hour)

	if got := s.StateAt(future); got != StateInvalid {
		t.Errorf("state = %q, want invalid", got)
	}
	if s.AllowsAt(future, FeatureGuardrails) {
		t.Error("an unreadable license granted a feature")
	}
}

// The missing state has no document and no term, so no clock makes it grant
// anything.
func TestMissingStaysMissingAtEveryInstant(t *testing.T) {
	var s Status
	for _, at := range []time.Time{{}, time.Now().UTC(), time.Now().UTC().Add(1e6 * time.Hour)} {
		if got := s.StateAt(at); got != StateMissing {
			t.Errorf("at %v: state = %q", at, got)
		}
		if s.AllowsAt(at, FeatureGuardrails) {
			t.Errorf("at %v: a missing license granted a feature", at)
		}
	}
	if !s.ExpiresAt().IsZero() {
		t.Errorf("ExpiresAt() = %v, want zero", s.ExpiresAt())
	}
}
