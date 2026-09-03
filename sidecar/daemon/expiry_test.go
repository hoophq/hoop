package daemon

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
	"github.com/hoophq/hoop/sidecar/policy"
)

// expiredIn signs a license whose term ends after d and puts it through the
// real verifier, so these tests age a verdict a customer could hold.
func expiredIn(t *testing.T, d time.Duration) license.Status {
	t.Helper()
	return licensetest.Status(t, licensetest.Expiring(d))
}

// The caps read the license live, so the same Status that lifted them gives
// them back when its term ends. Without this a process holds the caps it
// bought for as long as it stays up.
func TestTheCapsComeBackWhenTheTermEnds(t *testing.T) {
	cfg := overCap()

	useLicense(t, cfg, licensetest.Expiring(time.Hour))
	if problems := cfg.checkLimits(cfg.Licensing()); len(problems) != 0 {
		t.Fatalf("a current license did not lift the caps: %v", problems)
	}

	useLicense(t, cfg, licensetest.Expiring(-time.Hour))
	problems := cfg.checkLimits(cfg.Licensing())
	if len(problems) != 2 {
		t.Fatalf("an expired license kept the caps lifted: %v", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p, "expired") {
			t.Errorf("the message does not name the expiry: %v", p)
		}
	}
}

// The summary line and the admin endpoint both derive from the same verdict,
// so an operator reading either after expiry sees the free tier rather than
// the "unlimited" the process started with.
func TestTheReportedCapsFollowTheTerm(t *testing.T) {
	if got := LimitsSummary(expiredIn(t, time.Hour)); !strings.Contains(got, "unlimited") {
		t.Errorf("a current license did not report unlimited: %q", got)
	}
	got := LimitsSummary(expiredIn(t, -time.Hour))
	for _, want := range []string{"1 guardrail rule(s)", "1 data masking rule(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("an expired license did not report the free tier: %q", got)
		}
	}
	if c := capsFor(expiredIn(t, -time.Hour)); capJSON(c.guardrails) == nil {
		t.Error("/config would still serve null, meaning no cap, after expiry")
	}
}

// Only a config the free tier would refuse is worth watching. Taking down a
// process that never used its license is an outage with no revenue behind it.
func TestOnlyAnOverCapConfigDependsOnTheLicense(t *testing.T) {
	if !overCap().dependsOnLicense() {
		t.Error("a config over both caps does not depend on its license")
	}
	within := &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("only-one")}},
		Listeners:  []ListenerConfig{limitsLane("appdb", ":1")},
	}
	if within.dependsOnLicense() {
		t.Error("a config inside the free tier depends on a license")
	}
}

// The transition itself: the watchdog notices the term ending and closes its
// channel, which is what makes Run drain and exit.
func TestTheWatchdogFiresWhenTheTermEnds(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	expired := watchLicense(ctx, expiredIn(t, -time.Second), time.Millisecond, log)

	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog did not notice an expired term")
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") ||
		!strings.Contains(out, "expired") {
		t.Errorf("the transition was not logged as a warning: %s", out)
	}
}

// A term with time left keeps the relay up. A watchdog that fired early would
// be an outage generator.
func TestTheWatchdogWaitsWhileTheTermRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	expired := watchLicense(ctx, expiredIn(t, time.Hour), time.Millisecond, newTestLogger(&bytes.Buffer{}))

	select {
	case <-expired:
		t.Fatal("the watchdog stopped a relay whose license is current")
	case <-time.After(50 * time.Millisecond):
	}
}

// Shutdown must not read as an expiry. The channel stays open when the
// context ends, so Run reports a signal as a signal and exits zero.
func TestTheWatchdogIsSilentOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	expired := watchLicense(ctx, expiredIn(t, time.Hour), time.Hour, newTestLogger(&bytes.Buffer{}))
	cancel()

	select {
	case <-expired:
		t.Fatal("a signalled shutdown was reported as a license expiry")
	case <-time.After(50 * time.Millisecond):
	}
}

// The stop is abrupt, so the notice is what keeps it from being a surprise.
// It counts down in days and repeats only when the count changes.
func TestExpiryIsAnnouncedBeforeItHappens(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)
	lic := expiredIn(t, 3*24*time.Hour)

	day := noticeLicenseExpiry(lic, -1, log)
	if day != 2 && day != 3 {
		t.Fatalf("days_left = %d, want 2 or 3", day)
	}
	first := buf.String()
	if !strings.Contains(first, "expires soon") || !strings.Contains(first, "days_left") {
		t.Errorf("no countdown was logged: %s", first)
	}

	buf.Reset()
	if again := noticeLicenseExpiry(lic, day, log); again != day {
		t.Errorf("the day count moved without time passing: %d then %d", day, again)
	}
	if buf.Len() != 0 {
		t.Errorf("the same day was announced twice: %s", buf.String())
	}
}

// Outside the notice window there is nothing to say. A line a day for two
// years is a line nobody reads.
func TestALongTermIsNotAnnounced(t *testing.T) {
	var buf bytes.Buffer
	noticeLicenseExpiry(expiredIn(t, 365*24*time.Hour), -1, newTestLogger(&buf))
	if buf.Len() != 0 {
		t.Errorf("a term with a year left was announced: %s", buf.String())
	}
}
