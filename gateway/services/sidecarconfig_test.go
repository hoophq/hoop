package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

// TestValidateGuardrailRuleForSidecar pins the whole refusal table. Each row is
// a rule an admin can save today and that would reach a sidecar as nothing at
// all, or take the sidecar's whole configuration down.
func TestValidateGuardrailRuleForSidecar(t *testing.T) {
	const okInput = `{"rules":[{"type":"deny_words_list","words":["DROP TABLE"]}]}`

	for _, tt := range []struct {
		name   string
		input  string
		output string
		want   string // substring of the refusal; empty means the rule is accepted
	}{
		{
			name:  "a deny words rule both engines speak",
			input: okInput,
		},
		{
			name:  "a pattern RE2 accepts",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":"(?i)^\\s*DELETE\\b"}]}`,
		},
		{
			// The sidecar has no output guardrail: it denies requests and masks
			// responses. Serving one as a request rule would deny statements the
			// admin meant to redact.
			name:   "an output rule has no sidecar equivalent",
			input:  okInput,
			output: `{"rules":[{"type":"pattern_match","pattern_regex":"[0-9]+"}]}`,
			want:   "output rules",
		},
		{
			// Six of the sidecar's eight rule types are unknown to the gateway
			// page. Storing one would ship {"type":"table","words":null} to an
			// agent, which matches nothing forever.
			name:  "a rule type only the sidecar knows",
			input: `{"rules":[{"type":"table","words":["customers"]}]}`,
			want:  "rule type",
		},
		{
			// The sidecar compiles patterns when it LOADS the document, so one
			// bad pattern refuses the whole configuration. The gateway compiles
			// at match time, which is why this saves clean today. Five of the
			// nine seeded rulepacks carry exactly this.
			name:  "a PCRE lookahead RE2 cannot compile",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":"(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
			want:  "RE2",
		},
		{
			name:  "a deny words rule with no words",
			input: `{"rules":[{"type":"deny_words_list","words":[]}]}`,
			want:  "no words",
		},
		{
			name:  "a pattern rule with no pattern",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":""}]}`,
			want:  "no pattern",
		},
		{
			name:  "a rule with nothing on the input side",
			input: `{"rules":[]}`,
			want:  "no input rules",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGuardrailRuleForSidecar("my-rule", json.RawMessage(tt.input), json.RawMessage(tt.output))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want the rule accepted, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want a refusal naming %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("refusal does not name %q: %v", tt.want, err)
			}
			// Whatever the verdict, the message names the rule, because an
			// admin reading it has several.
			if err != nil && !strings.Contains(err.Error(), "my-rule") {
				t.Errorf("refusal does not name the rule: %v", err)
			}
		})
	}
}

// TestGuardrailRulesToPolicy pins the translation itself: what the sidecar
// receives for a rule the gateway stored.
func TestGuardrailRulesToPolicy(t *testing.T) {
	rules, err := guardrailRulesToPolicy("no-drop", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["DROP TABLE"],"message":"ask the data team"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want one rule, got %d", len(rules))
	}
	r := rules[0]
	// The name reaches the sidecar's audit rows and its deny message, so it
	// carries the gateway rule's name rather than an index.
	if r.Name != "no-drop" {
		t.Errorf("name = %q, want no-drop", r.Name)
	}
	if string(r.Type) != "deny_words_list" {
		t.Errorf("type = %q", r.Type)
	}
	if len(r.Words) != 1 || r.Words[0] != "DROP TABLE" {
		t.Errorf("words = %v", r.Words)
	}
	if r.Message != "ask the data team" {
		t.Errorf("message = %q, want the operator's own", r.Message)
	}

	// One gateway rule holding several entries becomes several sidecar rules,
	// and each needs its own name: the audit row and the max_calls budget key
	// on it, so two rules sharing a name report as one.
	many, err := guardrailRulesToPolicy("mixed", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["A"]},{"type":"deny_words_list","words":["B"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(many) != 2 || many[0].Name == many[1].Name {
		t.Fatalf("want two distinctly named rules, got %+v", many)
	}
}

// TestAppendGuardrailsPreservesTheOptOut guards the one merge subtlety that is
// invisible in the type. `rules: []` is how a lane says "none of the defaults";
// composing must add to it, never replace it, or a lane that opted out silently
// starts enforcing again.
func TestAppendGuardrailsPreservesTheOptOut(t *testing.T) {
	// An opt-out is an empty but NON-nil slice: that is exactly the
	// distinction the daemon's own merge reads, and it does not survive a
	// length test.
	optedOut := &daemon.GuardrailsConfig{Rules: []policy.Rule{}}
	added, err := guardrailRulesToPolicy("r", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["X"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := appendGuardrails(optedOut, added)
	if len(out.Rules) != 1 {
		t.Fatalf("want the bound rule appended to the opt-out, got %d rules", len(out.Rules))
	}
	// The caller's block must not be mutated: the stored row is shared with
	// whatever else reads it this request.
	if len(optedOut.Rules) != 0 {
		t.Errorf("the source block was mutated: %+v", optedOut.Rules)
	}

	// An absent block is created rather than skipped.
	fresh := appendGuardrails(nil, added)
	if fresh == nil || len(fresh.Rules) != 1 {
		t.Errorf("want a block created for a listener that had none, got %+v", fresh)
	}
}

// ---------------------------------------------------------------------------
// Data masking
// ---------------------------------------------------------------------------

func TestValidateDataMaskingRuleForSidecar(t *testing.T) {
	ssn := []models.SupportedEntityTypesEntry{{Name: "IDENTITY", EntityTypes: []string{"US_SSN"}}}

	for _, tt := range []struct {
		name      string
		supported []models.SupportedEntityTypesEntry
		custom    []models.CustomEntityTypesEntry
		want      string
	}{
		{
			name:      "entity types both engines detect",
			supported: ssn,
		},
		{
			// The sidecar's pii section selects and ignores BUILT-IN
			// recognizers and cannot register a regex of its own, so storing
			// the rule and serving the rest would mask less than configured.
			name:      "a custom entity type has no sidecar recognizer",
			supported: ssn,
			custom:    []models.CustomEntityTypesEntry{{Name: "BADGE", Regex: "B-[0-9]+"}},
			want:      "custom entity types",
		},
		{
			name:      "a rule that names no entity at all",
			supported: []models.SupportedEntityTypesEntry{{Name: "EMPTY"}},
			want:      "no entity types",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDataMaskingRuleForSidecar("mask-pii", tt.supported, tt.custom)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want the rule accepted, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want a refusal naming %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("refusal does not name %q: %v", tt.want, err)
			}
		})
	}
}

// TestMaskRulesForFlattensGroups pins the translation: the gateway groups
// entity types under a display name the sidecar rule has no field for, so the
// group name is dropped and duplicates across groups collapse.
func TestMaskRulesForFlattensGroups(t *testing.T) {
	raw, err := maskRulesFor([]models.MaskBinding{{
		RuleName: "mask-pii",
		SupportedEntityTypes: models.SupportedEntityTypesList{
			{Name: "IDENTITY", EntityTypes: []string{"US_SSN", "EMAIL_ADDRESS"}},
			{Name: "CONTACT", EntityTypes: []string{"EMAIL_ADDRESS", "PHONE_NUMBER"}},
		},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []maskRule
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the composed rules are not a rule list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one rule, got %d", len(got))
	}
	if got[0].Name != "mask-pii" {
		t.Errorf("name = %q, want the gateway rule's own", got[0].Name)
	}
	want := []string{"US_SSN", "EMAIL_ADDRESS", "PHONE_NUMBER"}
	if len(got[0].Entities) != len(want) {
		t.Fatalf("entities = %v, want %v deduplicated across groups", got[0].Entities, want)
	}
	for i := range want {
		if got[0].Entities[i] != want[i] {
			t.Errorf("entities = %v, want %v", got[0].Entities, want)
			break
		}
	}
}

// TestMaskThresholdFor pins the one value a sidecar cannot hold twice. The
// detector is built once per process from the pii section, so two bound rules
// asking for different sensitivities has no correct answer and picking one
// would mask at a threshold nobody configured.
func TestMaskThresholdFor(t *testing.T) {
	f := func(v float64) *float64 { return &v }

	if got, err := maskThresholdFor([]models.MaskBinding{
		{RuleName: "a"}, {RuleName: "b", ScoreThreshold: f(0.7)},
	}); err != nil || got == nil || *got != 0.7 {
		// A nil threshold is "unset", not zero: alcatraz reads zero as "use
		// the default", so a rule that leaves it blank does not compete.
		t.Fatalf("want the one set threshold, got %v (err=%v)", got, err)
	}

	if got, err := maskThresholdFor([]models.MaskBinding{
		{RuleName: "a", ScoreThreshold: f(0.5)}, {RuleName: "b", ScoreThreshold: f(0.5)},
	}); err != nil || got == nil || *got != 0.5 {
		t.Fatalf("two rules agreeing is not a conflict: got %v (err=%v)", got, err)
	}

	_, err := maskThresholdFor([]models.MaskBinding{
		{RuleName: "loose", ScoreThreshold: f(0.3)}, {RuleName: "strict", ScoreThreshold: f(0.9)},
	})
	if err == nil {
		t.Fatal("want a refusal for two different thresholds on one sidecar")
	}
	for _, name := range []string{"loose", "strict"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal must name both rules, it omits %q: %v", name, err)
		}
	}
}

// TestWithPIIThresholdKeepsTheOperatorsSection guards the seam where the
// control plane writes into a section the detector plugin owns: only the one
// key it owns may change.
func TestWithPIIThresholdKeepsTheOperatorsSection(t *testing.T) {
	raw, err := withPIIThreshold(json.RawMessage(
		`{"ignored":["DATE_TIME"],"allow_list":["acme.test"],"language":"en"}`), 0.8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("not an object: %v", err)
	}
	if got["threshold"] != 0.8 {
		t.Errorf("threshold = %v, want 0.8", got["threshold"])
	}
	for _, k := range []string{"ignored", "allow_list", "language"} {
		if got[k] == nil {
			t.Errorf("the operator's %q key was dropped: %v", k, got)
		}
	}
}

// ---------------------------------------------------------------------------
// AI session analyzer
// ---------------------------------------------------------------------------

// TestAnalyzerBlockFor pins what a distributed analyzer rule may and may not
// change on a lane: the rule owns the risk decision and the prompt, the
// operator keeps the cost controls.
func TestAnalyzerBlockFor(t *testing.T) {
	base := &daemon.LaneAnalyzerConfig{
		MaxCalls: 40, TimeoutSec: 12, Send: "statement", LowRisk: "allow",
	}
	prompt := "Treat the payments schema as high risk."
	out, err := analyzerBlockFor("risky-writes", models.AISessionAnalyzerRiskEvaluation{
		HighRiskAction:   models.BlockExecution,
		MediumRiskAction: models.AllowExecution,
	}, &prompt, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.HighRisk != "block" || out.MediumRisk != "allow" {
		t.Errorf("risk actions = %q/%q, want block/allow", out.HighRisk, out.MediumRisk)
	}
	if out.Prompt != prompt {
		t.Errorf("prompt = %q, want the rule's", out.Prompt)
	}
	// The lane's own budget is the operator's, and a rule distributed to a
	// fleet has no business overwriting it.
	if out.MaxCalls != 40 || out.TimeoutSec != 12 || out.Send != "statement" {
		t.Errorf("the lane's cost controls were overwritten: %+v", out)
	}
	if base.HighRisk != "" {
		t.Errorf("the source block was mutated: %+v", base)
	}

	// A lane with no analyzer block has no provider and no trigger, so
	// composing risk actions onto it is refused rather than served.
	if _, err := analyzerBlockFor("risky-writes", models.AISessionAnalyzerRiskEvaluation{}, nil, nil); err == nil {
		t.Error("want a refusal for a listener with no analyzer block")
	}
}

// TestRequireAccessRequestIsRefused pins the one action that crosses on paper
// and not in the runtime. A sidecar declares require_review in its config and
// refuses it at startup, so serving it would kill every sidecar that
// reschedules and leave the running ones on stale rules.
func TestRequireAccessRequestIsRefused(t *testing.T) {
	err := ValidateAnalyzerRuleForSidecar("needs-review", models.AISessionAnalyzerRiskEvaluation{
		HighRiskAction: models.RequireAccessRequest,
	})
	if err == nil {
		t.Fatal("want a refusal for require_access_request")
	}
	// The refusal names the ticket, so the next person to read it knows the
	// restriction has an end date rather than being a design position.
	if !strings.Contains(err.Error(), "EVL-289") {
		t.Errorf("the refusal must name the ticket that lifts it: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The two invariants the fleet rests on
// ---------------------------------------------------------------------------

// boundEverything is one sidecar with a rule of every kind bound to it, in
// both scopes. The two tests below compose it and check what came out.
func boundEverything() (daemon.Config, []models.BoundRule, []models.MaskBinding, []models.AnalyzerBinding) {
	threshold := 0.7
	prompt := "Treat the payments schema as high risk."
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}},
		{Name: "reporting", Protocol: "mysql", Listen: ":3306", Upstream: "warehouse:3306"},
	}}
	return cfg,
		[]models.BoundRule{
			{RuleName: "fleet-wide", Input: json.RawMessage(
				`{"rules":[{"type":"deny_words_list","words":["TRUNCATE"]}]}`)},
			{RuleName: "appdb-only", ListenerName: "appdb", Input: json.RawMessage(
				`{"rules":[{"type":"pattern_match","pattern_regex":"(?i)^\\s*DROP\\b"}]}`)},
		},
		[]models.MaskBinding{
			{RuleName: "mask-pii", ListenerName: "appdb", ScoreThreshold: &threshold,
				SupportedEntityTypes: models.SupportedEntityTypesList{
					{Name: "IDENTITY", EntityTypes: []string{"US_SSN"}}}},
		},
		[]models.AnalyzerBinding{
			{RuleName: "risky-writes", ListenerName: "appdb", CustomPrompt: &prompt,
				RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
					HighRiskAction: models.BlockExecution}},
		}
}

// TestComposedDocumentStillDecodesStrictly is the test that stands between this
// feature and a bricked fleet.
//
// A sidecar decodes its document with DisallowUnknownFields at three layers: a
// key it does not declare refuses the WHOLE configuration, and the sidecar
// cannot recover on its own. Composition must therefore only ever write keys
// daemon.Config already has -- which is also why the control plane's own strict
// parser is what checks it here, rather than a hand-listed set.
func TestComposedDocumentStillDecodesStrictly(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()
	composed, err := foldSidecarRules(cfg, guardrails, masking, analyzers)
	if err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	raw, err := json.Marshal(composed)
	if err != nil {
		t.Fatalf("the composed document does not marshal: %v", err)
	}
	if _, err := ParseSidecarConfiguration(raw); err != nil {
		t.Fatalf("the composed document would be refused by a sidecar: %v\ndocument: %s", err, raw)
	}

	// And the rules actually landed where the daemon reads them.
	if composed.Guardrails == nil || len(composed.Guardrails.Rules) != 1 {
		t.Errorf("the sidecar-wide guardrail is missing from the top-level block: %+v", composed.Guardrails)
	}
	appdb := composed.Listeners[0]
	if appdb.Guardrails == nil || len(appdb.Guardrails.Rules) != 1 {
		t.Errorf("the listener-scoped guardrail is missing from its lane: %+v", appdb.Guardrails)
	}
	if appdb.Mask == nil || len(appdb.Mask.Rules) == 0 {
		t.Errorf("the masking rule is missing from its lane: %+v", appdb.Mask)
	}
	if appdb.Analyzer == nil || appdb.Analyzer.HighRisk != "block" {
		t.Errorf("the analyzer rule is missing from its lane: %+v", appdb.Analyzer)
	}
	if appdb.Analyzer.MaxCalls != 40 {
		t.Errorf("the lane's call budget was overwritten by a distributed rule: %+v", appdb.Analyzer)
	}
	// A lane nothing was bound to is left exactly as its author wrote it.
	if composed.Listeners[1].Guardrails != nil || composed.Listeners[1].Mask != nil {
		t.Errorf("an unbound lane was modified: %+v", composed.Listeners[1])
	}
}

// TestCompositionNeverReachesTheBaseline is the invariant the whole design
// rests on.
//
// A sidecar hot-swaps the rule sections with no dropped connection, and
// RESTARTS for anything else. If composition ever wrote outside those
// sections, every rule edit would become a fleet restart -- the opposite of
// what configuring from a control plane is for. daemon.BaselineDoc is the
// authority on where that line is, so the test asks it rather than repeating
// the list.
func TestCompositionNeverReachesTheBaseline(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()

	before, err := daemon.BaselineDoc(&cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	composed, err := foldSidecarRules(cfg, guardrails, masking, analyzers)
	if err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	after, err := daemon.BaselineDoc(&composed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("composition changed the restart-bound baseline, so every rule edit "+
			"would restart the fleet\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestCompositionLeavesTheStoredDocumentAlone pins the other half of
// compose-on-read: the row the request loaded is shared, and folding in place
// would make the next handshake compose an already-composed document.
func TestCompositionLeavesTheStoredDocumentAlone(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()
	stored, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := foldSidecarRules(cfg, guardrails, masking, analyzers); err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	again, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stored) != string(again) {
		t.Fatalf("the stored document was mutated by composing it\nbefore: %s\nafter:  %s", stored, again)
	}
}

// TestCompositionRefusesARuleBoundToAMissingListener pins the failure mode a
// rename creates. Skipping the binding would serve a document enforcing less
// than the admin sees bound, silently; the refusal reaches the handshake,
// which a sidecar survives by keeping the rules it already has.
func TestCompositionRefusesARuleBoundToAMissingListener(t *testing.T) {
	cfg, guardrails, _, _ := boundEverything()
	cfg.Listeners[0].Name = "appdb-renamed"

	_, err := foldSidecarRules(cfg, guardrails, nil, nil)
	if err == nil {
		t.Fatal("want a refusal for a rule bound to a listener that no longer exists")
	}
	for _, want := range []string{"appdb-only", "appdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rule and the listener, it omits %q: %v", want, err)
		}
	}
}
