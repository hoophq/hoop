package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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

// aiBlock is an analyzer block whose high level has action and AI rule
// options r.
func aiBlock(action string, r LevelAIRule) *LaneAnalyzerConfig {
	la := laneBlock()
	la.HighRisk = action
	la.AIRules = &LevelAIRules{High: &r}
	return la
}

// aiLane is a licensed postgres lane whose high level is warn and lets the
// AI apply rule_library rules.
func aiLane(t *testing.T, r LevelAIRule) *Config {
	t.Helper()
	cfg := blockLane(aiBlock("warn", r))
	cfg.RuleLibrary = aiLibrary()
	if err := cfg.UseLicense(licensetest.Ref(t, licensetest.Enterprise())); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// ai_rules decodes strictly beside the string levels and round-trips; the
// accessor reads each level.
func TestAIRulesDecodeBesideTheLevels(t *testing.T) {
	doc := `{"high":"warn","ai_rules":{"high":{"guardrails":{"mode":"existing_and_new","rules":["no-deletes"]},` +
		`"mask":{"rules":[]}}}}`
	var la LaneAnalyzerConfig
	if err := json.Unmarshal([]byte(doc), &la); err != nil {
		t.Fatal(err)
	}
	r := la.LevelAIRule(analyzer.RiskHigh)
	if r == nil || r.Guardrails.Mode != "existing_and_new" || !slices.Equal(r.Guardrails.Rules, []string{"no-deletes"}) ||
		r.Mask == nil || r.Mask.Rules == nil || len(r.Mask.Rules) != 0 {
		t.Fatalf("decoded %+v", r)
	}
	if la.LevelAIRule(analyzer.RiskMedium) != nil || la.LevelAIRule(analyzer.RiskLow) != nil ||
		(*LaneAnalyzerConfig)(nil).LevelAIRule(analyzer.RiskHigh) != nil {
		t.Error("an unset level has AI rule options")
	}
	if raw, _ := json.Marshal(la); string(raw) != doc {
		t.Errorf("encoded %s\nwant    %s", raw, doc)
	}
	// Absent rules (every library rule) and rules: [] (none) stay apart.
	var absent LaneAnalyzerConfig
	if err := json.Unmarshal([]byte(`{"ai_rules":{"low":{"mask":{}}}}`), &absent); err != nil {
		t.Fatal(err)
	}
	if absent.LevelAIRule(analyzer.RiskLow).Mask.Rules != nil {
		t.Error("absent rules decoded as a list")
	}
	strict := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
		"analyzer":{"high":"warn","ai_rules":{"high":{"guardrails":{"mode":"existing_only","names":["x"]}}}}}]}`
	if _, err := LoadConfigBytes([]byte(strict)); err == nil || !strings.Contains(err.Error(), "names") {
		t.Errorf("an unknown key inside guardrails loaded: %v", err)
	}
}

// Each refusal of ai_rules, the warnings that depend on rule_library and the
// protocol, and the library refusals.
func TestEachAIRuleSettingIsRefusedOrWarned(t *testing.T) {
	aiProblems := func(p []string) []string {
		return slices.DeleteFunc(slices.Clone(p), func(s string) bool { return !strings.Contains(s, "ai_rules") })
	}
	t.Run("unknown mode is refused", func(t *testing.T) {
		la := laneBlock()
		la.MediumRisk = "warn"
		la.AIRules = &LevelAIRules{Medium: &LevelAIRule{Mask: &AIRuleOptions{Mode: "existing_and_some"}}}
		problems := ValidateLaneAnalyzerBlock(la, "appdb")
		if !slices.ContainsFunc(problems, func(p string) bool {
			return strings.Contains(p, `ai_rules.medium: mask.mode "existing_and_some" is unknown`)
		}) {
			t.Errorf("problems = %v", problems)
		}
		for _, ok := range []string{"", "existing_only", "existing_and_new"} {
			la.AIRules.Medium = &LevelAIRule{Guardrails: &AIRuleOptions{Mode: ok}}
			if p := ValidateLaneAnalyzerBlock(la, "appdb"); len(p) != 0 {
				t.Errorf("mode %q refused: %v", ok, p)
			}
		}
	})
	t.Run("a level whose action applies no AI rule is refused", func(t *testing.T) {
		for _, action := range []string{"block", "defer"} {
			la := aiBlock(action, LevelAIRule{Guardrails: &AIRuleOptions{}, Mask: &AIRuleOptions{}})
			p := ValidateLaneAnalyzerBlock(la, "appdb")
			if !slices.ContainsFunc(p, func(s string) bool {
				return strings.Contains(s, fmt.Sprintf(`ai_rules.high lets the AI apply rules, and the high `+
					`risk action %q applies none`, action))
			}) {
				t.Errorf("%s: problems = %v", action, p)
			}
			if len(la.aiRuleLevels()) != 0 || la.usesAIMask() {
				t.Errorf("a %s level applies AI rules", action)
			}
		}
		for _, action := range []string{"", "allow", "warn", "require_approval", "require_review", "ai-handoff"} {
			la := aiBlock(action, LevelAIRule{Guardrails: &AIRuleOptions{}})
			la.ApprovalRule = "dba"
			la.Handoffs = &LevelHandoffs{High: &LevelHandoff{AllowedActions: []string{"allow", "require_approval"}}}
			if action != "ai-handoff" {
				la.Handoffs = nil
			}
			if p := aiProblems(ValidateLaneAnalyzerBlock(la, "appdb")); len(p) != 0 {
				t.Errorf("%q refused: %v", action, p)
			}
			if len(la.aiRuleLevels()) != 1 {
				t.Errorf("%q: the level applies no AI rule", action)
			}
		}
	})
	t.Run("an entry with neither guardrails nor mask is refused", func(t *testing.T) {
		p := ValidateLaneAnalyzerBlock(aiBlock("warn", LevelAIRule{}), "appdb")
		if len(p) != 1 || !strings.Contains(p[0], "ai_rules.high sets neither guardrails nor mask") {
			t.Errorf("problems = %v", p)
		}
	})
	t.Run("an unknown name warns and is ignored", func(t *testing.T) {
		la := aiBlock("warn", LevelAIRule{Guardrails: &AIRuleOptions{Rules: []string{"no-deletes", "ghost"}}})
		w := LaneAIRuleWarnings(aiLibrary(), la, blockLane(la).Listeners[0])
		if len(w) != 1 || !strings.Contains(w[0], `ai_rules.high.guardrails.rules names "ghost", which rule_library does not hold`) {
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
	t.Run("a config file refuses at load", func(t *testing.T) {
		for ai, want := range map[string]string{
			`"high":"warn","ai_rules":{"high":{"guardrails":{"mode":"all"}}}`: `guardrails.mode "all" is unknown`,
			`"high":"block","ai_rules":{"high":{"guardrails":{}}}`:            `action "block" applies none`,
			`"high":"warn","ai_rules":{"high":{}}`:                            "sets neither guardrails nor mask",
		} {
			doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
				"analyzer":{` + ai + `}}],
				"rule_library":{"guardrails":[{"name":"g","type":"deny_words_list","words":["drop"]}]}}`
			if _, err := LoadConfigBytes([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s loaded: %v", ai, err)
			}
		}
	})
}

// A build without analyzer_ai_rules is refused ai_rules and the library,
// with no downgrade; this build is served them. An ai_rules block that
// names no level is not served.
func TestAIRulesNeedTheirCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityAIRules) {
		t.Fatalf("this build does not report %s", CapabilityAIRules)
	}
	without := slices.DeleteFunc(SidecarCapabilities(), func(c string) bool { return c == CapabilityAIRules })
	old := Handshake{Version: "1.200.0", Capabilities: without}
	full := Handshake{Version: "1.200.0", Capabilities: SidecarCapabilities()}
	for name, cfg := range map[string]Config{
		"level options": *aiLane(t, LevelAIRule{Mask: &AIRuleOptions{}}),
		"library only":  {RuleLibrary: aiLibrary(), Listeners: pgLane().Listeners},
	} {
		t.Run(name, func(t *testing.T) {
			if err := CheckServable(cfg, old); err == nil || !strings.Contains(err.Error(), CapabilityAIRules) {
				t.Errorf("served to a build without the capability: %v", err)
			}
			if err := CheckServable(cfg, full); err != nil {
				t.Errorf("refused to this build: %v", err)
			}
		})
	}
	empty := blockLane(laneBlock())
	empty.Listeners[0].Analyzer.AIRules = &LevelAIRules{}
	if err := CheckServable(*empty, old); err != nil {
		t.Errorf("an ai_rules block with no level was served: %v", err)
	}
}

// Without an Enterprise license granting the feature, the config is refused;
// the library rules count against no free-tier cap.
func TestAIRulesNeedAnEnterpriseLicense(t *testing.T) {
	cfg := aiLane(t, LevelAIRule{Guardrails: &AIRuleOptions{}, Mask: &AIRuleOptions{}})
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
	cfg := aiLane(t, LevelAIRule{
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

// An AI guardrail gets the lane validation a configured guardrail gets: a pii
// entity the detector does not find is refused, as checkPIIEntities refuses
// it in a guardrails block. A library rule is left out with a note; a
// created one is recorded in ai_rule_invalid and never applies.
func TestAnAIGuardrailNamingAnInactiveEntityIsInvalid(t *testing.T) {
	cfg := aiLane(t, LevelAIRule{Guardrails: &AIRuleOptions{Mode: "existing_and_new"}})
	cfg.RuleLibrary.Guardrails = append(cfg.RuleLibrary.Guardrails,
		policy.Rule{Name: "cards", Type: policy.MatchPII, Entities: []string{"CREDIT_CARD"}, Message: "no cards"})
	agent := &ruleAgent{calls: []analyzer.ToolCall{toolCall("create_guardrail",
		`{"type":"pii","entities":["CREDIT_CARD"],"message":"no cards","reason":"cards"}`)}}
	det := stubPlugin{entities: []string{"EMAIL_ADDRESS"}}
	lanes, err := buildLanes(cfg, det, &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agent, det: det})
	if err != nil {
		t.Fatal(err)
	}
	ln := lanes[0]
	if !slices.ContainsFunc(ln.notes, func(n string) bool {
		return strings.Contains(n, `guardrail "cards" is invalid`) && strings.Contains(n, "CREDIT_CARD")
	}) {
		t.Errorf("notes %v do not refuse the library rule", ln.notes)
	}
	v := policy.Chain{ln.policy}.EvaluateWith(deleteFromUsers(), &policy.EvalContext{})
	if v.Denied {
		t.Fatalf("a guardrail on an inactive entity applied: %+v", v)
	}
	var invalid []map[string]any
	if err := json.Unmarshal([]byte(v.Annotations[analyzer.MetadataAIRuleInvalid]), &invalid); err != nil ||
		len(invalid) != 1 || !strings.Contains(fmt.Sprint(invalid[0]["error"]), "CREDIT_CARD") {
		t.Errorf("ai_rule_invalid = %q", v.Annotations[analyzer.MetadataAIRuleInvalid])
	}
	if got := v.Annotations[analyzer.MetadataAIRules]; got != "[]" {
		t.Errorf("ai_rules = %q, want []", got)
	}
	for _, tool := range agent.tools[0] {
		if tool.Name == "apply_guardrail" && !slices.Equal(tool.Input.Properties["rule"].Enum, []string{"no-deletes"}) {
			t.Errorf("guardrail enum = %v", tool.Input.Properties["rule"].Enum)
		}
	}
}

// An ssh lane neither offers nor applies a guardrail type an ssh statement
// cannot answer: a library table rule is left out, create_guardrail does not
// offer table, and the compiler refuses one.
func TestAnSSHLaneTakesNoTableGuardrail(t *testing.T) {
	tables := policy.Rule{Name: "orders-only", Type: policy.MatchTable, Tables: []string{"orders"}, Message: "m"}
	lib := aiLibrary()
	lib.Guardrails = append(lib.Guardrails, tables)
	la := aiBlock("warn", LevelAIRule{Guardrails: &AIRuleOptions{Mode: "existing_and_new"}})
	out, notes, err := buildLaneAIRules(lib, la, ListenerConfig{Name: "box", Protocol: "ssh", Analyzer: la},
		stubPlugin{entities: []string{"EMAIL_ADDRESS"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, `guardrail "orders-only" is invalid`) }) {
		t.Errorf("notes %v do not refuse the table rule", notes)
	}
	g := out.levels[analyzer.RiskHigh].Guardrails
	if len(g.Rules) != 1 || g.Rules[0].Name != "no-deletes" {
		t.Errorf("rules = %v", g.Rules)
	}
	if slices.Contains(g.Types, policy.MatchTable) || !slices.Contains(g.Types, policy.MatchPattern) {
		t.Errorf("create_guardrail types = %v", g.Types)
	}
	tables.Name = "ai-guardrail"
	if _, err := out.compileGuardrail(tables); err == nil || !strings.Contains(err.Error(), "ssh") {
		t.Errorf("compiled a table guardrail on an ssh lane: %v", err)
	}
}

// A mask option with no detector refuses the lane, as a mask block does.
func TestAnAIMaskNeedsADetector(t *testing.T) {
	cfg := aiLane(t, LevelAIRule{Mask: &AIRuleOptions{}})
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
		la := aiBlock("warn", LevelAIRule{Mask: &AIRuleOptions{Mode: "existing_and_new"}})
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

// A grpc lane takes an AI mask only where it decodes every response field:
// not raw (declared fields only) and not discover.fail_open (forwards what
// it cannot decode). The warning names the reason.
func TestAGRPCLaneTakesAnAIMaskOnlyWhereItDecodesEveryField(t *testing.T) {
	for name, tc := range map[string]struct {
		grpc *GRPCCodecConfig
		want gate.MaskScope
		warn string
	}{
		"descriptors": {grpc: &GRPCCodecConfig{Descriptors: DescriptorPaths{"a.pb"}}, want: gate.MaskScopeExchange},
		"discover":    {grpc: &GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection"}}, want: gate.MaskScopeExchange},
		"discover, fail open": {grpc: &GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection", FailOpen: true}},
			want: gate.MaskScopeNone, warn: "grpc.discover.fail_open"},
		"raw": {grpc: &GRPCCodecConfig{Raw: &GRPCRawConfig{StringFields: []GRPCRawStringFields{
			{Method: "/a.B/C", Response: []string{"1"}}}}}, want: gate.MaskScopeNone, warn: "grpc.raw.string_fields declares"},
		"no schema": {want: gate.MaskScopeNone, warn: "cannot tie a response"},
	} {
		la := aiBlock("warn", LevelAIRule{Mask: &AIRuleOptions{}})
		lc := ListenerConfig{Name: "l", Protocol: "grpc", GRPC: tc.grpc, Analyzer: la}
		if got := laneMaskScope(lc); got != tc.want {
			t.Errorf("%s: scope %v, want %v", name, got, tc.want)
		}
		w := LaneAIRuleWarnings(nil, la, lc)
		if tc.warn == "" && len(w) != 0 || tc.warn != "" && !slices.ContainsFunc(w, func(s string) bool {
			return strings.Contains(s, tc.warn) && strings.Contains(s, "is denied")
		}) {
			t.Errorf("%s: warnings %v, want %q", name, w, tc.warn)
		}
	}
}

// rule_library and the level options are rule content: a change swaps, and
// never reaches the restart baseline.
func TestTheRuleLibraryIsRuleContent(t *testing.T) {
	a := aiLane(t, LevelAIRule{Mask: &AIRuleOptions{}})
	b := aiLane(t, LevelAIRule{Mask: &AIRuleOptions{}})
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
