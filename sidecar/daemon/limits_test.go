package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
)

// overCap builds a config that authors two guardrail rules and two mask
// rules, which is one of each over the free tier.
func overCap() *Config {
	lane := limitsLane("appdb", ":1")
	lane.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule("no-drop-on-appdb")}}
	lane.Mask = &MaskConfig{Rules: maskRule("US_SSN")}
	return &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("no-drop")}},
		Mask:       &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners:  []ListenerConfig{lane},
	}
}

// licenseStatus builds the verdict a daemon runs under. Assembled rather
// than signed, because the daemon consumes a verdict and never a document.
// sidecar/license owns a key it can sign with and settles signatures there;
// forging one here would test that package twice and this one not at all.
func licenseStatus(state license.State, typ string, features []string, expires time.Time) license.Status {
	return license.Status{
		State:  state,
		Source: "the test",
		License: &license.License{
			Payload: license.Payload{
				Type:         typ,
				IssuedAt:     time.Now().Add(-time.Hour).Unix(),
				ExpireAt:     expires.Unix(),
				AllowedHosts: []string{"*"},
				Description:  "Acme Corp",
				Features:     features,
			},
			KeyID:     "test-key",
			Signature: "test-signature",
		},
	}
}

// licensed is a current enterprise license. No features named means every
// feature, which is what most licenses carry.
func licensed(features ...string) license.Status {
	return licenseStatus(license.StateValid, license.EnterpriseType, features,
		time.Now().Add(720*time.Hour))
}

// limitsLane is a minimal valid listener, so a limits test fails on the limit
// it is about and not on a missing upstream.
func limitsLane(name, listen string) ListenerConfig {
	return ListenerConfig{
		Name: name, Protocol: "postgres", Listen: listen, Upstream: "h:5432",
	}
}

func maskRule(entity string) []byte {
	return []byte(`[{"name":"r","entity":"` + entity + `","strategy":"redact"}]`)
}

// The cap fires on the second guardrail rule and the message says where both
// of them are. buildLanes rather than (*Config).Validate: the file validator
// runs before the license resolves, so the caps moved to the one function
// every caller that runs or validates a sidecar reaches.
func TestSecondGuardrailRuleIsRefused(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule("no-drop-on-appdb")}}
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("no-drop")}},
		Listeners:  []ListenerConfig{lane},
	}

	_, err := buildLanes(cfg, nil, nil)
	if err == nil {
		t.Fatal("two guardrail rules were accepted")
	}
	for _, want := range []string{"2 guardrail rules", "guardrails: 1", "appdb: 1", "at most 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// The deprecated `policy` block folds onto `guardrails` before anything
// validates, so the cap counts the same rule once however it was spelled. A
// counter reading both fields would refuse a one-rule config written the old
// way.
func TestDeprecatedPolicySpellingCountsOnce(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "policy": {"rules": [{"name":"no-drop","type":"operation","operations":["drop"]}]}
    }`)

	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("one rule in the deprecated spelling was refused: %v", err)
	}
}

// One rule is one rule however many lanes inherit it. resolve() copies the
// defaults into every lane, so a cap counting RESOLVED lanes would report
// this config as two rules and refuse a free-tier config that holds one.
func TestOneGlobalRuleAcrossTwoLanesIsAccepted(t *testing.T) {
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("no-drop")}},
		Listeners:  []ListenerConfig{limitsLane("a", ":1"), limitsLane("b", ":2")},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("one global rule inherited by two lanes was refused: %v", err)
	}
}

// ai_analysis rules share a slice with guardrails and are not guardrails.
// They leave the process and cost money per statement, and their controls are
// the trigger and the max_calls budget, not a count. Capping the slice would
// cap them by accident.
func TestAIAnalysisRulesAreNotCountedAsGuardrails(t *testing.T) {
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{
			rule("no-drop"), aiRule("risky-1"), aiRule("risky-2"),
		}},
		Listeners: []ListenerConfig{limitsLane("appdb", ":1")},
	}

	if problems := cfg.checkLimits(license.Status{}); len(problems) != 0 {
		t.Errorf("ai_analysis rules counted against the guardrail cap: %v", problems)
	}
}

// Masking is capped the same way and across the same scope. mask.rules
// REPLACES rather than concatenates, so a global block and a lane block are
// two authored rules even though no single byte path sees both.
func TestSecondMaskRuleIsRefused(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Mask = &MaskConfig{Rules: maskRule("US_SSN")}
	cfg := &Config{
		Mask:      &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners: []ListenerConfig{lane},
	}

	_, err := buildLanes(cfg, nil, nil)
	if err == nil {
		t.Fatal("two data masking rules were accepted")
	}
	for _, want := range []string{"2 data masking rules", "mask: 1", "appdb: 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// `rules: []` is how a lane switches inherited masking off, so it authors
// nothing and must not spend the one rule this build allows. The count is a
// decode rather than len() on the raw JSON, which those two bytes would pass.
func TestLaneOptingOutOfMaskingCostsNothing(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Mask = &MaskConfig{Rules: []byte(`[]`)}
	cfg := &Config{
		Mask:      &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners: []ListenerConfig{lane},
	}

	if problems := cfg.checkLimits(license.Status{}); len(problems) != 0 {
		t.Errorf("an empty override was counted as a rule: %v", problems)
	}
}

// A mask block that is not an array is named once, against the block that
// authored it. Reporting it per inheriting lane would print one typo as many
// times as there are listeners.
func TestMalformedMaskRulesAreReportedOnceAtTheirSite(t *testing.T) {
	cfg := &Config{
		Mask:      &MaskConfig{Rules: []byte(`{"entity":"US_SSN"}`)},
		Listeners: []ListenerConfig{limitsLane("a", ":1"), limitsLane("b", ":2")},
	}

	_, err := buildLanes(cfg, nil, nil)
	if err == nil {
		t.Fatal("a mask block that is not an array was accepted")
	}
	if n := strings.Count(err.Error(), "mask.rules: not a JSON array"); n != 1 {
		t.Errorf("reported %d times, want 1: %v", n, err)
	}
}

// The file validator must not enforce the caps: it runs before Setup
// resolves the license flag or HOOP_LICENSE, so a config a license makes
// legal would be refused before anyone read the license.
func TestTheCapsAreEnforcedAfterTheLicenseIsKnown(t *testing.T) {
	cfg := overCap()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("the file validator refused a config before the license was read: %v", err)
	}
	if _, err := buildLanes(cfg, nil, nil); err == nil {
		t.Fatal("buildLanes accepted a config over the caps")
	} else if !strings.Contains(err.Error(), "guardrail rules") {
		t.Errorf("error does not name the cap: %v", err)
	}
}

// Every problem in one run: a config over both caps reports both, and still
// reports the unrelated lane errors beside them. A cap that returned on the
// first violation would make an operator restart once per rule.
func TestLimitsAreReportedWithEveryOtherProblem(t *testing.T) {
	cfg := overCap()
	cfg.Listeners = append(cfg.Listeners, ListenerConfig{Name: "broken", Protocol: "postgres"})

	_, err := buildLanes(cfg, nil, nil)
	if err == nil {
		t.Fatal("an invalid config was accepted")
	}
	for _, want := range []string{"guardrail rules", "data masking rules", "broken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// The caps are reported, not just enforced. An operator who has to discover a
// limit by hitting it learns it in the worst place.
func TestLimitsSummaryNamesBothCaps(t *testing.T) {
	got := LimitsSummary(license.Status{})
	for _, want := range []string{"1 guardrail rule(s)", "1 data masking rule(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("LimitsSummary() = %q, missing %q", got, want)
		}
	}
}

// The whole point of the feature. A license Hoop signed lifts both caps, and
// the config that was refused a moment ago builds.
func TestALicenseLiftsBothCaps(t *testing.T) {
	cfg := overCap()
	cfg.UseLicense(licensed())

	if problems := cfg.checkLimits(cfg.Licensing()); len(problems) != 0 {
		t.Errorf("a licensed config was capped: %v", problems)
	}
}

// A feature list restricts. A license sold for data masking leaves the
// guardrail cap where it was, or a one-feature license pays for two.
func TestALicenseLiftsOnlyTheFeaturesItNames(t *testing.T) {
	cfg := overCap()
	cfg.UseLicense(licensed(license.FeatureDataMasking))

	problems := cfg.checkLimits(cfg.Licensing())
	if len(problems) != 1 {
		t.Fatalf("want exactly the guardrail problem, got %v", problems)
	}
	if !strings.Contains(problems[0], "guardrail rules") {
		t.Errorf("the wrong cap fired: %v", problems)
	}
	if !strings.Contains(problems[0], "does not cover this") {
		t.Errorf("the message does not say the license is the reason: %v", problems[0])
	}
}

// An oss license is signed, current, and grants nothing. Treating any
// verifying license as enterprise would hand the paid caps to the free tier.
func TestAnOSSLicenseLeavesTheCapsInForce(t *testing.T) {
	cfg := overCap()
	cfg.UseLicense(licenseStatus(license.StateValid, license.OSSType, nil,
		time.Now().Add(720*time.Hour)))

	if problems := cfg.checkLimits(cfg.Licensing()); len(problems) != 2 {
		t.Errorf("an oss license changed the caps: %v", problems)
	}
}

// An expired license drops the process back to the free tier, and the message
// has to say so. "Contact our support" for a config that loaded last week
// sends an operator to read their rules instead of their expiry date.
func TestAnExpiredLicenseRestoresTheCapsAndSaysWhy(t *testing.T) {
	cfg := overCap()
	cfg.UseLicense(licenseStatus(license.StateExpired, license.EnterpriseType, nil,
		time.Now().Add(-24*time.Hour)))

	problems := cfg.checkLimits(cfg.Licensing())
	if len(problems) != 2 {
		t.Fatalf("an expired license kept the caps lifted: %v", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p, "expired") || !strings.Contains(p, license.Support) {
			t.Errorf("the message does not point at the renewal: %v", p)
		}
	}
}

// Unlicensed, the cap message has to name every way a license can be
// supplied. It is the one moment the operator is looking at the limit.
func TestAnUncappedMessageNamesEveryLicenseSource(t *testing.T) {
	problems := overCap().checkLimits(license.Status{})
	if len(problems) == 0 {
		t.Fatal("the free tier accepted a config over both caps")
	}
	for _, want := range []string{"license flag", license.EnvVar, `"license" key`} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("the message does not mention %q: %v", want, problems[0])
		}
	}
}

// The summary is what -validate prints and what the startup log carries. A
// licensed process saying "1 guardrail rule(s)" while enforcing none is worse
// than printing nothing.
func TestLimitsSummaryReportsUnlimitedWhenLicensed(t *testing.T) {
	got := LimitsSummary(licensed())
	for _, want := range []string{"unlimited guardrail rule(s)", "unlimited data masking rule(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("LimitsSummary() = %q, missing %q", got, want)
		}
	}
}

// The admin endpoint serves the caps to somebody else's dashboard, where a
// -1 would read as a cap of minus one. Unlimited is null.
func TestCapJSONRendersUnlimitedAsNull(t *testing.T) {
	if got := capJSON(unlimited); got != nil {
		t.Errorf("capJSON(unlimited) = %v, want nil", *got)
	}
	got := capJSON(maxGuardrailRules)
	if got == nil || *got != 1 {
		t.Errorf("capJSON(1) = %v, want 1", got)
	}
}

func TestCountMaskRules(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{"absent", "", 0, false},
		{"null", "null", 0, false},
		{"empty array", "[]", 0, false},
		{"one rule", `[{"name":"a"}]`, 1, false},
		{"two rules", `[{"name":"a"},{"name":"b"}]`, 2, false},
		{"object not array", `{"name":"a"}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := countMaskRules([]byte(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("count = %d, want %d", got, tc.want)
			}
		})
	}
}
