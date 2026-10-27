package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
	"github.com/hoophq/hoop/sidecar/policy"
)

// ruleAgent rates every statement high and answers each Decide with calls.
type ruleAgent struct {
	highRiskProvider
	mu    sync.Mutex
	calls []analyzer.ToolCall
	tools [][]analyzer.ToolSpec
}

func (a *ruleAgent) Decide(_ context.Context, _, _ string, tools []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append(a.tools, tools)
	return a.calls, nil
}

func toolCall(name, args string) analyzer.ToolCall {
	return analyzer.ToolCall{Name: name, Args: json.RawMessage(args)}
}

func aiLibrary() *RuleLibrary {
	return &RuleLibrary{
		Guardrails: []policy.Rule{
			{Name: "no-deletes", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpDelete},
				Message: "deletes are not allowed"},
		},
		Mask: json.RawMessage(`[{"name":"emails","entities":["EMAIL_ADDRESS"]}]`),
	}
}

// aiLane is a licensed postgres lane whose high level lets the AI apply
// rule_library rules.
func aiLane(t *testing.T, high RiskLevel) *Config {
	t.Helper()
	la := laneBlock()
	la.HighRisk = high
	cfg := blockLane(la)
	cfg.RuleLibrary = aiLibrary()
	if err := cfg.UseLicense(licensetest.Ref(t, licensetest.Enterprise())); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The level fields decode strictly, round-trip, and need the object form.
func TestAIRuleOptionsDecodeOnALevel(t *testing.T) {
	doc := `{"action":"warn","guardrails":{"mode":"existing_and_new","rules":["no-deletes"]},"mask":{"rules":[]}}`
	var r RiskLevel
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatal(err)
	}
	if r.Guardrails.Mode != "existing_and_new" || !slices.Equal(r.Guardrails.Rules, []string{"no-deletes"}) ||
		r.Mask == nil || r.Mask.Rules == nil || len(r.Mask.Rules) != 0 {
		t.Fatalf("decoded %+v %+v", r.Guardrails, r.Mask)
	}
	if raw, _ := json.Marshal(r); string(raw) != doc {
		t.Errorf("encoded %s\nwant    %s", raw, doc)
	}
	// Absent rules (every library rule) and rules: [] (none) stay apart.
	var absent RiskLevel
	json.Unmarshal([]byte(`{"action":"warn","mask":{}}`), &absent)
	if absent.Mask.Rules != nil {
		t.Error("absent rules decoded as a list")
	}
	if err := json.Unmarshal([]byte(`{"action":"warn","guardrails":{"mode":"existing_only","names":["x"]}}`), &r); err == nil {
		t.Error("an unknown key inside guardrails loaded")
	}
}

// Each row of the ticket's table, plus the library refusals.
func TestEachAIRuleSettingIsWarnedOrRefused(t *testing.T) {
	mode := func(m string) RiskLevel {
		return RiskLevel{Action: "warn", Guardrails: &AIRuleOptions{Mode: m}}
	}
	t.Run("unknown mode is refused", func(t *testing.T) {
		la := laneBlock()
		la.MediumRisk = RiskLevel{Action: "warn", Mask: &AIRuleOptions{Mode: "existing_and_some"}}
		problems := ValidateLaneAnalyzerBlock(la, "appdb")
		if !slices.ContainsFunc(problems, func(p string) bool {
			return strings.Contains(p, `medium risk mask.mode "existing_and_some" is unknown`)
		}) {
			t.Errorf("problems = %v", problems)
		}
		for _, ok := range []string{"", "existing_only", "existing_and_new"} {
			la.MediumRisk = mode(ok)
			if p := ValidateLaneAnalyzerBlock(la, "appdb"); len(p) != 0 {
				t.Errorf("mode %q refused: %v", ok, p)
			}
		}
	})
	t.Run("a block level warns and ignores the fields", func(t *testing.T) {
		la := laneBlock()
		la.HighRisk = RiskLevel{Action: "block", Guardrails: &AIRuleOptions{}, Mask: &AIRuleOptions{}}
		if w := LaneAnalyzerWarnings(la); !slices.ContainsFunc(w, func(s string) bool {
			return strings.Contains(s, `high risk sets guardrails or mask, and its action "block" applies no AI rule`)
		}) {
			t.Errorf("warnings = %v", w)
		}
		if len(la.aiRuleLevels()) != 0 || la.usesAIMask() {
			t.Error("a block level applies AI rules")
		}
	})
	t.Run("an unknown name warns and is ignored", func(t *testing.T) {
		la := laneBlock()
		la.HighRisk = RiskLevel{Action: "warn", Guardrails: &AIRuleOptions{Rules: []string{"no-deletes", "ghost"}}}
		w := LaneAIRuleWarnings(aiLibrary(), la, blockLane(la).Listeners[0])
		if len(w) != 1 || !strings.Contains(w[0], `guardrails.rules names "ghost", which rule_library does not hold`) {
			t.Errorf("warnings = %v", w)
		}
	})
	t.Run("the library needs unique names", func(t *testing.T) {
		lib := &RuleLibrary{
			Guardrails: []policy.Rule{{Type: policy.MatchDenyWords, Words: []string{"x"}}, {Name: "a"}, {Name: "a"}},
			Mask:       json.RawMessage(`[{"entities":["EMAIL_ADDRESS"]},{"name":"m"},{"name":"m"}]`),
		}
		p := lib.validate()
		for _, want := range []string{"rule_library.guardrails[0] has no name", `guardrails names "a" twice`,
			"rule_library.mask[0] has no name", `mask names "m" twice`} {
			if !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, want) }) {
				t.Errorf("problems %v lack %q", p, want)
			}
		}
		if p := (&RuleLibrary{Mask: json.RawMessage(`{"name":"m"}`)}).validate(); len(p) != 1 {
			t.Errorf("a mask that is not a list: %v", p)
		}
	})
	t.Run("a config file refuses the mode at load", func(t *testing.T) {
		doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
			"analyzer":{"high":{"action":"warn","guardrails":{"mode":"all"}}}}],
			"rule_library":{"guardrails":[{"name":"g","type":"deny_words_list","words":["drop"]}]}}`
		if _, err := LoadConfigBytes([]byte(doc)); err == nil || !strings.Contains(err.Error(), `guardrails.mode "all" is unknown`) {
			t.Errorf("loaded: %v", err)
		}
	})
}

// A build without analyzer_ai_rules is refused the fields and the library,
// with no downgrade; this build is served them.
func TestAIRulesNeedTheirCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityAIRules) {
		t.Fatalf("this build does not report %s", CapabilityAIRules)
	}
	without := slices.DeleteFunc(SidecarCapabilities(), func(c string) bool { return c == CapabilityAIRules })
	rule := func(string) string { return "rule" }
	for name, cfg := range map[string]Config{
		"level options": *aiLane(t, RiskLevel{Action: "warn", Mask: &AIRuleOptions{}}),
		"library only":  {RuleLibrary: aiLibrary(), Listeners: pgLane().Listeners},
	} {
		t.Run(name, func(t *testing.T) {
			old := Handshake{Version: "1.200.0", Capabilities: without}
			if err := CheckServable(cfg, old); err == nil || !strings.Contains(err.Error(), CapabilityAIRules) {
				t.Errorf("served to a build without the capability: %v", err)
			}
			if _, err := DowngradeRiskLevels(cfg, old, rule); err == nil || !strings.Contains(err.Error(), CapabilityAIRules) {
				t.Errorf("downgraded: %v", err)
			}
			full := Handshake{Version: "1.200.0", Capabilities: SidecarCapabilities()}
			if err := CheckServable(cfg, full); err != nil {
				t.Errorf("refused to this build: %v", err)
			}
			if _, err := DowngradeRiskLevels(cfg, full, rule); err != nil {
				t.Errorf("refused by the downgrade: %v", err)
			}
		})
	}
}

// Without an Enterprise license granting the feature, the config is refused;
// the library rules count against no free-tier cap.
func TestAIRulesNeedAnEnterpriseLicense(t *testing.T) {
	cfg := aiLane(t, RiskLevel{Action: "warn", Guardrails: &AIRuleOptions{}, Mask: &AIRuleOptions{}})
	free := cfg.CheckLimits(license.Status{})
	for _, want := range []string{"AI-applied guardrails", "AI-applied data masking"} {
		if !slices.ContainsFunc(free, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("free tier problems %v lack %q", free, want)
		}
	}
	if !cfg.dependsOnLicense() {
		t.Error("the process would not stop when the license lapses")
	}
	if p := cfg.CheckLimits(licensetest.Status(t, licensetest.Enterprise())); len(p) != 0 {
		t.Errorf("an Enterprise license refused it: %v", p)
	}
	guardOnly := cfg.CheckLimits(licensetest.Status(t, licensetest.Enterprise(license.FeatureGuardrails)))
	if len(guardOnly) != 1 || !strings.Contains(guardOnly[0], "data masking") {
		t.Errorf("a guardrails-only license: %v", guardOnly)
	}
	if _, err := buildLanes(cfg, stubPlugin{entities: []string{"EMAIL_ADDRESS"}},
		&analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: &ruleAgent{}}); err != nil {
		t.Errorf("a licensed lane did not build: %v", err)
	}
	cfg.lic = license.Status{}
	if _, err := buildLanes(cfg, stubPlugin{entities: []string{"EMAIL_ADDRESS"}},
		&analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: &ruleAgent{}}); err == nil {
		t.Error("an unlicensed lane built")
	}
}

// End to end through buildLanes: the analyzer gets the library rules the
// level selects, an invalid library rule is warned about and left out, and
// the lane's gates run under the protocol's mask scope.
func TestALaneResolvesItsAIRulesAgainstTheLibrary(t *testing.T) {
	cfg := aiLane(t, RiskLevel{Action: "warn",
		Guardrails: &AIRuleOptions{},
		Mask:       &AIRuleOptions{Mode: "existing_and_new", Rules: []string{"emails", "ghost"}}})
	cfg.RuleLibrary.Guardrails = append(cfg.RuleLibrary.Guardrails,
		policy.Rule{Name: "bad-regex", Type: policy.MatchPattern, Pattern: "("},
		policy.Rule{Name: "deferred", Type: policy.MatchDenyWords, Words: []string{"x"}, Action: policy.ActionDefer})
	agent := &ruleAgent{calls: []analyzer.ToolCall{toolCall("apply_mask", `{"rule":"emails","reason":"emails"}`)}}
	lanes, err := buildLanes(cfg, stubPlugin{entities: []string{"EMAIL_ADDRESS"}},
		&analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agent})
	if err != nil {
		t.Fatal(err)
	}
	ln := lanes[0]
	if ln.maskScope != gate.MaskScopeSerial {
		t.Errorf("mask scope = %v", ln.maskScope)
	}
	for _, want := range []string{`"ghost", which rule_library does not hold`, `guardrail "bad-regex" is invalid`,
		`guardrail "deferred" does not deny on a match`} {
		if !slices.ContainsFunc(ln.notes, func(n string) bool { return strings.Contains(n, want) }) {
			t.Errorf("notes %v lack %q", ln.notes, want)
		}
	}
	ec := &policy.EvalContext{}
	v := policy.Chain{ln.policy}.EvaluateWith(deleteFromUsers(), ec)
	if v.Denied || len(ec.ResponseMasks) != 1 {
		t.Fatalf("verdict %+v, %d mask(s)", v, len(ec.ResponseMasks))
	}
	tools := agent.tools[0]
	for _, tool := range tools {
		if tool.Name == "apply_guardrail" && !slices.Equal(tool.Input.Properties["rule"].Enum, []string{"no-deletes"}) {
			t.Errorf("guardrail enum = %v", tool.Input.Properties["rule"].Enum)
		}
	}
	if !slices.ContainsFunc(tools, func(s analyzer.ToolSpec) bool { return s.Name == "create_mask" }) {
		t.Errorf("tools = %v", tools)
	}
}

// A mask option with no detector refuses the lane, as a mask block does.
func TestAnAIMaskNeedsADetector(t *testing.T) {
	cfg := aiLane(t, RiskLevel{Action: "warn", Mask: &AIRuleOptions{}})
	_, err := buildLanes(cfg, nil, &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: &ruleAgent{}})
	if err == nil || !strings.Contains(err.Error(), "no detection plugin") {
		t.Errorf("built: %v", err)
	}
}

// Each protocol's scope, and the warning where it is none.
func TestEachProtocolHasItsAIMaskScope(t *testing.T) {
	for proto, want := range map[string]gate.MaskScope{
		"postgres": gate.MaskScopeSerial, "mysql": gate.MaskScopeSerial, "mssql": gate.MaskScopeNone,
		"oracle": gate.MaskScopeSerial, "clickhouse": gate.MaskScopeSerial, "http": gate.MaskScopeHTTP,
		"mongodb": gate.MaskScopeNone, "ssh": gate.MaskScopeNone, "grpc": gate.MaskScopeNone,
	} {
		lc := ListenerConfig{Name: "l", Protocol: proto}
		if got := aiMaskScope(lc); got != want {
			t.Errorf("%s: scope %v, want %v", proto, got, want)
		}
		la := laneBlock()
		la.HighRisk = RiskLevel{Action: "warn", Mask: &AIRuleOptions{Mode: "existing_and_new"}}
		w := LaneAIRuleWarnings(nil, la, lc)
		warned := slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, "cannot tie a response") })
		if warned != (want == gate.MaskScopeNone) {
			t.Errorf("%s: warnings %v", proto, w)
		}
		if laneMaskScope(ListenerConfig{Protocol: proto, Analyzer: laneBlock()}) != gate.MaskScopeNone {
			t.Errorf("%s: a lane without AI masks runs a mask scope", proto)
		}
	}
}

// rule_library and the level options are rule content: a change swaps, and
// never reaches the restart baseline.
func TestTheRuleLibraryIsRuleContent(t *testing.T) {
	a := aiLane(t, RiskLevel{Action: "warn", Mask: &AIRuleOptions{}})
	b := aiLane(t, RiskLevel{Action: "warn", Mask: &AIRuleOptions{}})
	b.RuleLibrary.Mask = json.RawMessage(`[{"name":"emails","entities":["PHONE_NUMBER"]}]`)
	da, _ := nonRuleDoc(a)
	db, _ := nonRuleDoc(b)
	if string(da) != string(db) {
		t.Error("a library change reached the baseline")
	}
	la, _ := laneRuleDoc(a, a.Listeners[0])
	lb, _ := laneRuleDoc(b, b.Listeners[0])
	if string(la) == string(lb) {
		t.Error("a library change does not swap the lane")
	}
}
