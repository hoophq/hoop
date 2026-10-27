package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
)

func guardrailOn(listener, name, input, output string) models.BoundRule {
	return models.BoundRule{RuleName: name, ListenerName: listener, AgentRule: models.AgentRule{
		Input: json.RawMessage(input), Output: json.RawMessage(output)}}
}

func maskingOn(listener, name string, entities []string, threshold *float64, custom ...models.CustomEntityTypesEntry) models.BoundRule {
	return models.BoundRule{RuleName: name, ListenerName: listener, AgentRule: models.AgentRule{
		SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: entities}},
		CustomEntityTypes:    custom, ScoreThreshold: threshold}}
}

func twoLanes() daemon.Config {
	return daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
		{Name: "shell", Protocol: "ssh", Listen: ":2222", Upstream: "host:22"},
	}}
}

func refusedWith(t *testing.T, err error, want string) {
	t.Helper()
	var refused ErrSidecarGatewayRuleRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want ErrSidecarGatewayRuleRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want a reason naming %q, got %q", want, err.Error())
	}
}

// A deny_words_list keeps the agent's case-sensitive match: it becomes a
// pattern_match on the quoted words, never the sidecar's own deny_words_list.
func TestADenyWordsListBecomesACaseSensitivePattern(t *testing.T) {
	cfg, err := foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("appdb", "no-drop",
			`{"rules":[{"type":"deny_words_list","words":["DROP TABLE","","a.b*"],"pattern_regex":"","message":"ask the DBA"}]}`,
			`{"rules":[]}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lane := cfg.Listeners[0]
	if lane.Guardrails == nil || len(lane.Guardrails.Rules) != 1 {
		t.Fatalf("want one translated rule, got %+v", lane.Guardrails)
	}
	got := lane.Guardrails.Rules[0]
	if got.Type != policy.MatchPattern || got.Pattern != `DROP TABLE|a\.b\*` || got.Name != "no-drop" || got.Message != "ask the DBA" {
		t.Errorf("translation = %+v", got)
	}
	rules, err := policy.NewRules(lane.Guardrails.Rules)
	if err != nil {
		t.Fatal(err)
	}
	if !rules.Evaluate(statement("DROP TABLE users")).Denied {
		t.Error("the denied word in the agent's case passed")
	}
	if rules.Evaluate(statement("drop table users")).Denied {
		t.Error("the sidecar denied a case the agent allows")
	}
	if cfg.Listeners[1].Guardrails != nil {
		t.Error("a lane the rule does not reach was modified")
	}
}

func statement(text string) inspect.Statement {
	return inspect.Statement{Text: text}
}

// A pattern_match stays one, and several entries keep their order under
// numbered names.
func TestAPatternMatchStaysOne(t *testing.T) {
	cfg, err := foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("appdb", "writes",
			`{"rules":[{"type":"pattern_match","words":[],"pattern_regex":"(?i)^\\s*delete"},`+
				`{"type":"pattern_match","pattern_regex":""},`+
				`{"type":"deny_words_list","words":["TRUNCATE"]}]}`, ``),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := cfg.Listeners[0].Guardrails.Rules
	if len(rules) != 2 || rules[0].Name != "writes/1" || rules[0].Pattern != `(?i)^\s*delete` ||
		rules[1].Name != "writes/3" || rules[1].Pattern != "TRUNCATE" {
		t.Errorf("translation = %+v", rules)
	}
}

// Translated rules land AFTER the listener's native rules, whose position
// decides which rule a statement is denied by.
func TestTranslatedRulesComeAfterNativeOnes(t *testing.T) {
	cfg := twoLanes()
	cfg.Listeners[0].Guardrails = &daemon.GuardrailsConfig{Rules: []policy.Rule{
		{Name: "native", Type: policy.MatchOperation, Operations: []inspect.Operation{"drop"}},
	}}
	out, err := foldGatewayRules(cfg, "pay", []models.BoundRule{
		guardrailOn("appdb", "gateway", `{"rules":[{"type":"deny_words_list","words":["x"]}]}`, ``),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := out.Listeners[0].Guardrails.Rules
	if len(rules) != 2 || rules[0].Name != "native" || rules[1].Name != "gateway" {
		t.Errorf("order = %+v", rules)
	}
	if len(cfg.Listeners[0].Guardrails.Rules) != 1 {
		t.Error("the stored document was modified in place")
	}
}

// An output rule is refused whole: a sidecar checks statements, not results,
// and a rule applied in part is the failure the ticket names.
func TestAnOutputRuleIsRefused(t *testing.T) {
	_, err := foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("appdb", "mask-ssn",
			`{"rules":[{"type":"deny_words_list","words":["x"]}]}`,
			`{"rules":[{"type":"pattern_match","pattern_regex":"\\d{3}-\\d{2}-\\d{4}"}]}`),
	}, nil)
	refusedWith(t, err, `guardrail rule "mask-ssn", bound to listener "appdb", has output rules`)
}

// A rule type the agent does not run is refused, and so is a pattern the
// sidecar's engine cannot compile: the sidecar's own check runs over the
// translation, as it does over a native block.
func TestATranslatedBlockPassesTheNativeChecks(t *testing.T) {
	_, err := foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("appdb", "odd", `{"rules":[{"type":"something_else","words":["x"]}]}`, ``),
	}, nil)
	refusedWith(t, err, `type "something_else"`)

	_, err = foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("appdb", "lookahead", `{"rules":[{"type":"pattern_match","pattern_regex":"^(?!x)"}]}`, ``),
	}, nil)
	refusedWith(t, err, "bad pattern")
}

// A listener the configuration no longer has is a refusal, as it is for a
// native rule: the configuration write that removes a bound listener is
// refused first, so this is the handshake's backstop.
func TestARuleOnAGoneListenerIsRefused(t *testing.T) {
	_, err := foldGatewayRules(twoLanes(), "pay", []models.BoundRule{
		guardrailOn("renamed", "r", `{"rules":[{"type":"deny_words_list","words":["x"]}]}`, ``),
	}, nil)
	refusedWith(t, err, `guardrail rule "r" is bound to listener "renamed", which this sidecar's configuration no longer has`)
}

// A masking rule's entity types become ONE mask rule with strategy mask.
func TestAMaskingRuleBecomesOneMaskRule(t *testing.T) {
	cfg, err := foldGatewayRules(twoLanes(), "pay", nil, []models.BoundRule{
		maskingOn("shell", "pii", []string{"EMAIL_ADDRESS", "US_SSN", "EMAIL_ADDRESS"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	lane := cfg.Listeners[1]
	if lane.Mask == nil {
		t.Fatal("no mask block on the lane")
	}
	var rules []maskRule
	if err := json.Unmarshal(lane.Mask.Rules, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Name != "pii" || rules[0].Strategy != "mask" ||
		strings.Join(rules[0].Entities, ",") != "EMAIL_ADDRESS,US_SSN" {
		t.Errorf("translation = %+v", rules)
	}
	// The sidecar's own masker accepts the block on its detector.
	det, err := alcatraz.DetectorFromConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := det.BuildMasker(lane.Mask.Rules); err != nil {
		t.Errorf("the sidecar refuses the translated block: %v", err)
	}
	if cfg.Listeners[0].Mask != nil {
		t.Error("a lane the rule does not reach was modified")
	}
}

// Custom entity types have no recognizer on a sidecar: refused.
func TestACustomEntityTypeIsRefused(t *testing.T) {
	_, err := foldGatewayRules(twoLanes(), "pay", nil, []models.BoundRule{
		maskingOn("appdb", "ids", []string{"EMAIL_ADDRESS"}, nil,
			models.CustomEntityTypesEntry{Name: "ORDER_ID", Regex: "ORD-\\d+", Score: 0.9}),
	})
	refusedWith(t, err, `data masking rule "ids", bound to listener "appdb", has custom entity types`)
}

// An entity the sidecar's detector does not load is refused: PERSON needs the
// ner module, and the pii section can also leave one out.
func TestAnEntityTheDetectorDoesNotLoadIsRefused(t *testing.T) {
	_, err := foldGatewayRules(twoLanes(), "pay", nil, []models.BoundRule{
		maskingOn("appdb", "names", []string{"PERSON"}, nil),
	})
	refusedWith(t, err, `names entity "PERSON"`)

	cfg := twoLanes()
	cfg.PII = json.RawMessage(`{"entities":["EMAIL_ADDRESS"]}`)
	_, err = foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "ssn", []string{"US_SSN"}, nil),
	})
	refusedWith(t, err, `names entity "US_SSN"`)
}

// A score threshold under the sidecar's own is refused: the sidecar would
// mask less than the rule asks. At or above it, and absent, pass.
func TestAThresholdBelowTheDetectorsIsRefused(t *testing.T) {
	low, high := 0.3, 0.5
	_, err := foldGatewayRules(twoLanes(), "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, &low),
	})
	refusedWith(t, err, "score threshold of 0.3, below the 0.4")

	if _, err := foldGatewayRules(twoLanes(), "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, &high),
	}); err != nil {
		t.Errorf("a threshold above the sidecar's was refused: %v", err)
	}

	cfg := twoLanes()
	cfg.PII = json.RawMessage(`{"threshold":0.6}`)
	_, err = foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, &high),
	})
	refusedWith(t, err, "below the 0.6")

	cfg.PII = json.RawMessage(`{"threshold":-1}`)
	if _, err := foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, &low),
	}); err != nil {
		t.Errorf("a sidecar with no threshold refused a rule: %v", err)
	}
}

// An entity a native mask rule already masks stays with that rule. The
// translated rule carries the rest, after the native block; a rule with
// nothing left adds no block.
func TestANativeMaskRuleKeepsItsEntity(t *testing.T) {
	cfg := twoLanes()
	cfg.Listeners[0].Mask = &daemon.MaskConfig{Rules: json.RawMessage(
		`[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]`)}
	out, err := foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS", "US_SSN"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var rules []maskRule
	if err := json.Unmarshal(out.Listeners[0].Mask.Rules, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Name != "emails" || rules[0].Strategy != "redact" ||
		rules[1].Name != "pii" || strings.Join(rules[1].Entities, ",") != "US_SSN" {
		t.Errorf("composed mask = %+v", rules)
	}
	det, _ := alcatraz.DetectorFromConfig(nil)
	if _, err := det.BuildMasker(out.Listeners[0].Mask.Rules); err != nil {
		t.Errorf("the sidecar refuses the composed block: %v", err)
	}

	cfg.Listeners[0].Mask = &daemon.MaskConfig{Rules: json.RawMessage(
		`[{"name":"emails","entity":"EMAIL_ADDRESS"}]`)}
	out, err = foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Listeners[0].Mask.Rules) != string(cfg.Listeners[0].Mask.Rules) {
		t.Errorf("a rule with nothing left to mask changed the lane: %s", out.Listeners[0].Mask.Rules)
	}
}

// A lane with no block of its own inherits the top-level one; the
// translation keeps it rather than replacing it.
func TestAnInheritedMaskBlockSurvives(t *testing.T) {
	cfg := twoLanes()
	cfg.Mask = &daemon.MaskConfig{Rules: json.RawMessage(`[{"name":"cards","entities":["CREDIT_CARD"]}]`)}
	out, err := foldGatewayRules(cfg, "pay", nil, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"US_SSN"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var rules []maskRule
	if err := json.Unmarshal(out.Listeners[0].Mask.Rules, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Name != "cards" || rules[1].Name != "pii" {
		t.Errorf("composed mask = %+v", rules)
	}
}

// The composed document still decodes strictly and passes the sidecar's
// checks: a translated block carries no key an older build refuses.
func TestATranslatedDocumentStillDecodesStrictly(t *testing.T) {
	base := daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
		{Name: "reporting", Protocol: "mysql", Listen: ":3306", Upstream: "warehouse:3306"},
	}}
	cfg, err := foldGatewayRules(base, "pay", []models.BoundRule{
		guardrailOn("appdb", "no-drop", `{"rules":[{"type":"deny_words_list","words":["DROP"],"message":"no"}]}`, ``),
		guardrailOn("reporting", "no-load", `{"rules":[{"type":"pattern_match","pattern_regex":"(?i)load data"}]}`, ``),
	}, []models.BoundRule{
		maskingOn("appdb", "pii", []string{"EMAIL_ADDRESS"}, nil),
		maskingOn("reporting", "pii", []string{"EMAIL_ADDRESS"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSidecarConfiguration(raw)
	if err != nil {
		t.Fatalf("a sidecar would refuse the composed document: %v\n%s", err, raw)
	}
	if err := CheckSidecarConfiguration(parsed); err != nil {
		t.Fatalf("the composed document fails the sidecar's checks: %v", err)
	}
	if err := daemon.CheckServable(parsed, daemon.Handshake{Capabilities: []string{"rule:pattern_match"}}); err != nil {
		t.Fatalf("a build that decodes pattern_match cannot be served the document: %v", err)
	}
}
