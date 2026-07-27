package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// mintUnsignedJWT builds a structurally valid JWT with the given exp.
// The daemon never verifies signatures (it received the token from the
// gateway), so a fake signature is fine for scheduling tests.
func mintUnsignedJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"sub": "u", "exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return fmt.Sprintf("%s.%s.%s", header, base64.RawURLEncoding.EncodeToString(payload), "sig")
}

func TestTokenStateRotationSemantics(t *testing.T) {
	ts := newTokenState()

	var hookCalls []string
	ts.SetRotationHook(func(tok string) { hookCalls = append(hookCalls, tok) })

	// SetLocal never fires the hook (state the service persisted itself).
	ts.SetLocal("initial")
	if len(hookCalls) != 0 {
		t.Fatal("SetLocal must not fire the rotation hook")
	}
	token, epoch := ts.Snapshot()
	if token != "initial" {
		t.Fatalf("Snapshot token: %q", token)
	}

	// A gateway rotation at the current epoch fires the hook exactly once.
	ts.Rotate("rotated-1", epoch)
	if ts.Current() != "rotated-1" || len(hookCalls) != 1 || hookCalls[0] != "rotated-1" {
		t.Fatalf("rotation not applied: current=%q hooks=%v", ts.Current(), hookCalls)
	}

	// Re-harvesting the same token (multiple REST calls in the rotation
	// window) must not rewrite the config again. Rotations do not bump
	// the epoch, so the original epoch is still valid.
	ts.Rotate("rotated-1", epoch)
	if len(hookCalls) != 1 {
		t.Fatalf("duplicate rotation must be a no-op, hooks=%v", hookCalls)
	}

	// Empty rotations are ignored.
	ts.Rotate("", epoch)
	if ts.Current() != "rotated-1" {
		t.Fatal("empty rotation must not clear the token")
	}

	// A follow-up rotation from the same session (same epoch) is accepted.
	ts.Rotate("rotated-2", epoch)
	if ts.Current() != "rotated-2" || len(hookCalls) != 2 {
		t.Fatalf("same-epoch follow-up rotation must apply: current=%q hooks=%v", ts.Current(), hookCalls)
	}
}

func TestTokenStateRejectsStaleRotationAfterLogout(t *testing.T) {
	ts := newTokenState()
	var hookCalls []string
	ts.SetRotationHook(func(tok string) { hookCalls = append(hookCalls, tok) })

	ts.SetLocal("session-A")
	_, epochA := ts.Snapshot()

	// Logout while a request from session A is still in flight.
	ts.SetLocal("")

	// The late response must NOT resurrect the dead session.
	ts.Rotate("rotated-from-A", epochA)
	if ts.Current() != "" || len(hookCalls) != 0 {
		t.Fatalf("stale rotation resurrected a dead session: current=%q hooks=%v", ts.Current(), hookCalls)
	}

	// Re-login as a different session; the stale rotation must not
	// clobber the new session's token either.
	ts.SetLocal("session-B")
	ts.Rotate("rotated-from-A", epochA)
	if ts.Current() != "session-B" || len(hookCalls) != 0 {
		t.Fatalf("stale rotation clobbered a new session: current=%q hooks=%v", ts.Current(), hookCalls)
	}

	// While a rotation at the CURRENT epoch is accepted normally.
	_, epochB := ts.Snapshot()
	ts.Rotate("rotated-from-B", epochB)
	if ts.Current() != "rotated-from-B" || len(hookCalls) != 1 {
		t.Fatalf("current-epoch rotation rejected: current=%q hooks=%v", ts.Current(), hookCalls)
	}
}

func TestJwtExpiry(t *testing.T) {
	exp := time.Now().Add(45 * time.Minute).Truncate(time.Second)
	got, ok := jwtExpiry(mintUnsignedJWT(t, exp))
	if !ok {
		t.Fatal("expected exp to parse")
	}
	if !got.Equal(exp) {
		t.Fatalf("exp mismatch: got %v want %v", got, exp)
	}

	if _, ok := jwtExpiry("opaque-token"); ok {
		t.Fatal("opaque token must not yield an expiry")
	}
	if _, ok := jwtExpiry("a.!!!.c"); ok {
		t.Fatal("invalid base64 payload must not yield an expiry")
	}
}

func TestNextRenewalDelay(t *testing.T) {
	newSvc := func(token string) *daemonService {
		ts := newTokenState()
		ts.SetLocal(token)
		return &daemonService{tokens: ts}
	}

	// isMinuteCadence matches the jittered in-window pacing (1m..1m30s).
	isMinuteCadence := func(d time.Duration) bool {
		return d >= time.Minute && d <= time.Minute+30*time.Second
	}

	// Logged out → short idle poll.
	if d := newSvc("").nextRenewalDelay(false); d != 30*time.Second {
		t.Fatalf("logged-out delay: %v", d)
	}

	// Opaque token → hourly.
	if d := newSvc("opaque").nextRenewalDelay(false); d != time.Hour {
		t.Fatalf("opaque delay: %v", d)
	}

	// Far-future expiry → sleep until renewalLead before it.
	far := mintUnsignedJWT(t, time.Now().Add(2*time.Hour))
	if d := newSvc(far).nextRenewalDelay(false); d < 2*time.Hour-renewalLead-time.Minute || d > 2*time.Hour-renewalLead {
		t.Fatalf("far-future delay out of range: %v", d)
	}

	// Inside the window (or already expired) → jittered 1-minute cadence.
	near := mintUnsignedJWT(t, time.Now().Add(2*time.Minute))
	if d := newSvc(near).nextRenewalDelay(false); !isMinuteCadence(d) {
		t.Fatalf("in-window delay: %v", d)
	}
	expired := mintUnsignedJWT(t, time.Now().Add(-time.Hour))
	if d := newSvc(expired).nextRenewalDelay(false); !isMinuteCadence(d) {
		t.Fatalf("expired delay: %v", d)
	}

	// Renewal disabled on the gateway → skip the pre-expiry window and
	// wake shortly after expiry instead.
	if d := newSvc(far).nextRenewalDelay(true); d < 2*time.Hour || d > 2*time.Hour+time.Minute {
		t.Fatalf("flag-off delay must land just past expiry: %v", d)
	}
	if d := newSvc(expired).nextRenewalDelay(true); !isMinuteCadence(d) {
		t.Fatalf("flag-off expired delay: %v", d)
	}
}

func TestRenewalFlagCacheResetsOnEpochChange(t *testing.T) {
	var c renewalFlagCache

	// Optimistic default at epoch 1.
	if c.disabledFor(1) {
		t.Fatal("cache must start optimistic (renewal enabled)")
	}

	// Gateway for session at epoch 1 reports the flag off.
	if flipped := c.observe(false, 1); !flipped {
		t.Fatal("enabled→disabled must report a flip")
	}
	if !c.disabledFor(1) {
		t.Fatal("flag-off observation must stick within the same epoch")
	}
	if c.observe(false, 1) {
		t.Fatal("repeated identical observation must not report a flip")
	}

	// Logout + re-login (epoch bump): the new session must NOT inherit
	// the previous session's flag-off state.
	if c.disabledFor(2) {
		t.Fatal("epoch change must reset the cache to optimistic")
	}

	// A late observation from the dead epoch must be ignored.
	if c.observe(false, 1) {
		t.Fatal("stale-epoch observation must be ignored")
	}
	if c.disabledFor(2) {
		t.Fatal("stale-epoch observation must not poison the new session")
	}
}
