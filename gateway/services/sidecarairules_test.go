package services

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aiRulesLane is a lane whose analyzer block, decoded as stored, is spec.
func aiRulesLane(t *testing.T, name, spec string) daemon.ListenerConfig {
	t.Helper()
	var block daemon.LaneAnalyzerConfig
	require.NoError(t, decodeSpec(json.RawMessage(spec), &block))
	return daemon.ListenerConfig{Name: name, Protocol: "postgres", Analyzer: &block}
}

var aiRulesLibrary = [len(aiRuleKinds)][]models.LibraryRule{
	aiKindGuardrails: {
		{Name: "multi", Spec: json.RawMessage(`{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]},` +
			`{"name":"no-truncate","type":"operation","operations":["truncate"]}]}`)},
		{Name: "no-delete", Spec: json.RawMessage(`{"rules":[{"name":"x","type":"operation","operations":["delete"]}]}`)},
		{Name: "unread", Spec: json.RawMessage(`{"rules":[{"name":"x","type":"operation","bogus":1}]}`)},
	},
	aiKindMask: {
		{Name: "emails", Spec: json.RawMessage(`{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"mask"}]}`)},
		{Name: "ssn", Spec: json.RawMessage(`{"rules":[{"columns":["ssn"],"strategy":"hash"}]}`)},
	},
}

func TestFoldRuleLibraryServesTheSelectedRules(t *testing.T) {
	stored := daemon.Config{Listeners: []daemon.ListenerConfig{
		aiRulesLane(t, "appdb", `{"high":"block","medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["multi","gone"]}}}}`),
		{Name: "plain", Protocol: "postgres"},
	}}
	before, err := json.Marshal(stored)
	require.NoError(t, err)

	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	require.NotNil(t, got.RuleLibrary)
	names := []string{}
	for _, r := range got.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"multi#1", "multi#2"}, names, "only the named row, one entry per rule")
	assert.Empty(t, got.RuleLibrary.Mask, "no level uses mask, so no mask rule is served")
	assert.Equal(t, []string{"multi#1", "multi#2", "gone"},
		got.Listeners[0].Analyzer.LevelAIRule(analyzer.RiskMedium).Guardrails.Rules,
		"the level names the served rules, and an unknown name stays for the sidecar to warn about")

	after, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after), "the stored document is not changed")
	// What is served decodes strictly, as a sidecar decodes it.
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var served daemon.Config
	require.NoError(t, decodeSpec(raw, &served))
	require.NotNil(t, served.RuleLibrary)
	assert.Len(t, served.RuleLibrary.Guardrails, 2)
}

func TestFoldRuleLibraryServesEveryRuleWhenRulesIsAbsent(t *testing.T) {
	stored := daemon.Config{Listeners: []daemon.ListenerConfig{
		aiRulesLane(t, "appdb", `{"low":"allow","medium":"warn","ai_rules":{"low":{"mask":{"mode":"existing_and_new"}},`+
			`"medium":{"guardrails":{"rules":["no-delete"]}}}}`),
	}}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	require.NotNil(t, got.RuleLibrary)
	require.Len(t, got.RuleLibrary.Guardrails, 1)
	assert.Equal(t, "no-delete", got.RuleLibrary.Guardrails[0].Name, "a one-rule row keeps the row's name")
	assert.JSONEq(t, `[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"},`+
		`{"name":"ssn","columns":["ssn"],"strategy":"hash"}]`, string(got.RuleLibrary.Mask),
		"every mask row, named as the row")
	assert.Nil(t, got.Listeners[0].Analyzer.LevelAIRule(analyzer.RiskLow).Mask.Rules, "absent stays absent: every library rule")

	// Another lane selecting every guardrail widens the library to all of
	// them; a row no sidecar decodes is left out.
	stored.Listeners = append(stored.Listeners,
		aiRulesLane(t, "other", `{"high":"require_approval","approvers":{"high":{"type":"ai_agent"}},`+
			`"ai_rules":{"high":{"guardrails":{}}}}`))
	got, err = foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	var names []string
	for _, r := range got.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"multi#1", "multi#2", "no-delete"}, names)
}

func TestFoldRuleLibraryLeavesADocumentWithoutAIRulesAlone(t *testing.T) {
	stored := daemon.Config{Listeners: []daemon.ListenerConfig{
		aiRulesLane(t, "appdb", `{"high":"block","medium":"warn"}`),
	}}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	assert.Nil(t, got.RuleLibrary)
}

func TestFoldRuleLibraryKeepsTheStoredLibraryNames(t *testing.T) {
	stored := daemon.Config{
		RuleLibrary: &daemon.RuleLibrary{Mask: json.RawMessage(`[{"name":"ssn","columns":["ssn"]}]`)},
		Listeners: []daemon.ListenerConfig{
			aiRulesLane(t, "appdb", `{"medium":"warn","ai_rules":{"medium":{"mask":{"rules":["emails"]}}}}`),
		},
	}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"ssn","columns":["ssn"]},{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"}]`,
		string(got.RuleLibrary.Mask), "the stored library is kept, the selected row added")

	// The org row "ssn" meets the stored "ssn": the stored rule keeps its
	// name, the row is served under another, and the level names that one.
	stored.Listeners = []daemon.ListenerConfig{aiRulesLane(t, "appdb",
		`{"medium":"warn","ai_rules":{"medium":{"mask":{"rules":["ssn","emails"]}}}}`)}
	got, err = foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err, "a name clash must not stop the configuration from reaching the sidecar")
	assert.JSONEq(t, `[{"name":"ssn","columns":["ssn"]},`+
		`{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"},`+
		`{"name":"ssn#dup1","columns":["ssn"],"strategy":"hash"}]`, string(got.RuleLibrary.Mask))
	assert.Equal(t, []string{"ssn#dup1", "emails"}, got.Listeners[0].Analyzer.LevelAIRule(analyzer.RiskMedium).Mask.Rules)
	assertServedNamesUnique(t, got)
}

// The reviewer's case and its relatives: row names are free text, so a
// multi-rule row's names must not meet any other row's name, nor a stored
// rule_library name, whatever the rows are called.
func TestFoldRuleLibraryNamesNeverCollide(t *testing.T) {
	two := `{"rules":[{"name":"email","type":"operation","operations":["drop"]},` +
		`{"name":"phone","type":"operation","operations":["delete"]}]}`
	one := `{"rules":[{"name":"x","type":"operation","operations":["update"]}]}`
	rows := [len(aiRuleKinds)][]models.LibraryRule{aiKindGuardrails: {
		{Name: "payments", Spec: json.RawMessage(two)},
		{Name: "payments/email", Spec: json.RawMessage(one)},
		{Name: "payments#1", Spec: json.RawMessage(one)},
		{Name: "payments#", Spec: json.RawMessage(two)},
		{Name: "payments##1", Spec: json.RawMessage(one)},
		{Name: "payments#dup1", Spec: json.RawMessage(one)},
	}}
	stored := daemon.Config{
		RuleLibrary: &daemon.RuleLibrary{Guardrails: []policy.Rule{
			{Name: "payments#1", Type: "operation", Operations: []inspect.Operation{"insert"}},
			{Name: "payments#1#dup1", Type: "operation", Operations: []inspect.Operation{"insert"}},
		}},
		Listeners: []daemon.ListenerConfig{aiRulesLane(t, "appdb", `{"medium":"warn","ai_rules":{"medium":{"guardrails":{}}}}`)},
	}
	got, err := foldRuleLibrary(stored, rows)
	require.NoError(t, err)
	var names []string
	for _, r := range got.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{
		"payments#1", "payments#1#dup1", // stored, unchanged
		"payments#1#dup2", "payments#2", // row "payments": "payments#1" and its first fallback are stored names
		"payments/email",               // row "payments/email"
		"payments##1",                  // row "payments#1"
		"payments###1", "payments###2", // row "payments#"
		"payments####1",  // row "payments##1"
		"payments##dup1", // row "payments#dup1"
	}, names)
	assertServedNamesUnique(t, got)

	// Each row is reachable by its own name.
	stored.Listeners = []daemon.ListenerConfig{aiRulesLane(t, "appdb",
		`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["payments","payments#1","payments/email"]}}}}`)}
	got, err = foldRuleLibrary(stored, rows)
	require.NoError(t, err)
	assertServedNamesUnique(t, got)
	assert.Equal(t, []string{"payments#1#dup2", "payments#2", "payments##1", "payments/email"},
		got.Listeners[0].Analyzer.LevelAIRule(analyzer.RiskMedium).Guardrails.Rules)
}

// assertServedNamesUnique checks the library as the sidecar does: no name
// twice, and the document still decodes strictly.
func assertServedNamesUnique(t *testing.T, cfg daemon.Config) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range cfg.RuleLibrary.Guardrails {
		assert.False(t, seen[r.Name], "guardrail %q is served twice", r.Name)
		seen[r.Name] = true
	}
	masks := map[string]bool{}
	if len(cfg.RuleLibrary.Mask) > 0 {
		var list []struct{ Name string }
		require.NoError(t, json.Unmarshal(cfg.RuleLibrary.Mask, &list))
		for _, m := range list {
			assert.False(t, masks[m.Name], "mask %q is served twice", m.Name)
			masks[m.Name] = true
		}
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var served daemon.Config
	require.NoError(t, decodeSpec(raw, &served))
}

func TestLibraryEntryNames(t *testing.T) {
	assert.Equal(t, []string{"row"}, libraryEntryNames("row", 1))
	assert.Equal(t, []string{"row#1", "row#2"}, libraryEntryNames("row", 2))
	assert.Equal(t, []string{"a##1"}, libraryEntryNames("a#1", 1))
	assert.Equal(t, []string{"a###1", "a###2"}, libraryEntryNames("a#", 2))
	assert.Empty(t, libraryEntryNames("row", 0))
}

// aiRulesBlock is an analyzer block whose medium level lets the AI apply
// every guardrail of the library.
func aiRulesBlock() daemon.LaneAnalyzerConfig {
	return daemon.LaneAnalyzerConfig{HighRisk: "block", MediumRisk: "warn",
		AIRules: &daemon.LevelAIRules{Medium: &daemon.LevelAIRule{Guardrails: &daemon.AIRuleOptions{}}}}
}

// ai_rules belong to the levels, so the rule's replace the listener's.
func TestAnAnalyzerRuleCarriesItsAIRules(t *testing.T) {
	base := &daemon.LaneAnalyzerConfig{MaxCalls: 10,
		AIRules: &daemon.LevelAIRules{Low: &daemon.LevelAIRule{Mask: &daemon.AIRuleOptions{}}}}

	got := mergeAnalyzerBlock(base, aiRulesBlock())
	assert.NotNil(t, got.LevelAIRule(analyzer.RiskMedium), "the rule's ai_rules are served")
	assert.Nil(t, got.LevelAIRule(analyzer.RiskLow), "the listener's ai_rules are replaced")
	assert.Equal(t, 10, got.MaxCalls, "the listener's max_calls is kept")

	got = mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{HighRisk: "block"})
	assert.Nil(t, got.AIRules, "a rule with no ai_rules keeps none of the listener's")
}

// An import moves ai_rules into the analyzer rule with the levels.
func TestSplitAnalyzerMovesTheAIRules(t *testing.T) {
	block := aiRulesBlock()
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{Name: "appdb", Protocol: "postgres", Analyzer: &block}}}
	stripped, items, err := SplitSidecarConfiguration("edge", cfg, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	var rule daemon.LaneAnalyzerConfig
	require.NoError(t, decodeSpec(items[0].Spec, &rule))
	assert.NotNil(t, rule.LevelAIRule(analyzer.RiskMedium), "the rule carries the ai_rules")
	for _, l := range stripped.Listeners {
		if l.Analyzer != nil {
			assert.Nil(t, l.Analyzer.AIRules, "the lane keeps no ai_rules")
		}
	}
}

// A block with only ai_rules decides: its unset levels allow, and the AI
// applies the rules on them. The import moves it as it moves any decision.
func TestSplitAnalyzerMovesAnAIRulesOnlyBlock(t *testing.T) {
	block := daemon.LaneAnalyzerConfig{MaxCalls: 10,
		AIRules: &daemon.LevelAIRules{Low: &daemon.LevelAIRule{Guardrails: &daemon.AIRuleOptions{}}}}
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{Name: "appdb", Protocol: "postgres", Analyzer: &block}}}
	stripped, items, err := SplitSidecarConfiguration("edge", cfg, nil)
	require.NoError(t, err)
	require.Len(t, items, 1, "the block is an analyzer rule")
	var rule daemon.LaneAnalyzerConfig
	require.NoError(t, decodeSpec(items[0].Spec, &rule))
	assert.Empty(t, rule.HighRisk+rule.MediumRisk+rule.LowRisk, "no action is set")
	assert.NotNil(t, rule.LevelAIRule(analyzer.RiskLow), "the rule carries the ai_rules")
	require.NotNil(t, stripped.Listeners[0].Analyzer)
	assert.Nil(t, stripped.Listeners[0].Analyzer.AIRules, "the lane keeps no ai_rules")
	assert.Equal(t, 10, stripped.Listeners[0].Analyzer.MaxCalls, "the lane keeps its overrides")
}

// No build without analyzer_ai_rules can apply an AI rule: the save refuses
// to serve it one, naming the capability.
func TestCheckSidecarCapabilitiesRefusesAIRulesToAnOldBuild(t *testing.T) {
	block := aiRulesBlock()
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{Name: "appdb", Protocol: "postgres", Analyzer: &block}}}
	old := slices.DeleteFunc(daemon.SidecarCapabilities(), func(c string) bool { return c == daemon.CapabilityAIRules })

	err := CheckSidecarCapabilities(&models.Sidecar{Name: "edge", Capabilities: old}, cfg)
	var missing ErrSidecarCapabilityMissing
	require.ErrorAs(t, err, &missing)
	assert.Contains(t, err.Error(), daemon.CapabilityAIRules)

	current := &models.Sidecar{Name: "edge", Capabilities: daemon.SidecarCapabilities()}
	assert.NoError(t, CheckSidecarCapabilities(current, cfg))
}

func TestPlanSidecarSessionAIRules(t *testing.T) {
	huge := strings.Repeat("y", 1<<16)
	var many []map[string]any
	for range 30 {
		many = append(many, map[string]any{"kind": "guardrail", "rule": "no-drop", "reason": "r", "matched": false})
	}
	manyRaw, err := json.Marshal(many)
	require.NoError(t, err)
	mixed, err := json.Marshal([]map[string]any{
		{"kind": "guardrail", "rule": huge, "reason": huge + "\x00", "matched": true},
		{"kind": "mask", "definition": map[string]any{"entities": []string{"EMAIL\x00_ADDRESS"}, "keep_last": 4},
			"reason": "emails", "matched": true},
		{"kind": "guardrail", "definition": json.RawMessage(`{"type":"pii","n":1e400}`), "reason": "big number"},
		{"kind": "unknown", "rule": "x", "reason": "r"},
	})
	require.NoError(t, err)
	invalid, err := json.Marshal([]map[string]any{
		{"kind": "mask", "definition": map[string]any{"columns": []string{huge}}, "reason": "r", "error": huge},
	})
	require.NoError(t, err)

	rated := func(seq int64, md map[string]string) daemon.SessionEvent {
		return sidecarEvent(seq, audit.KindStatement, time.Second, func(e *audit.Event) {
			e.Statement = "SELECT 1"
			md[analyzer.MetadataRiskLevel] = "medium"
			e.Metadata = md
		})
	}
	plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, existingSidecarSession(1), []daemon.SessionEvent{
		rated(2, map[string]string{analyzer.MetadataAIRules: string(manyRaw),
			analyzer.MetadataApprover: string(analyzer.ApproverAIAgent), analyzer.MetadataApprovalDecision: analyzer.DecisionRejected,
			analyzer.MetadataApprovalReasonCode: "unbounded_scope"}),
		rated(3, map[string]string{analyzer.MetadataAIRules: string(mixed),
			analyzer.MetadataAIRuleInvalid: string(invalid), analyzer.MetadataAIRulesError: huge}),
		rated(4, map[string]string{analyzer.MetadataAIRules: `[]`}),
		rated(5, map[string]string{analyzer.MetadataAIRules: `{"not":"a list"}`}),
	})
	require.NoError(t, err)
	require.Len(t, plan.AIDecisions, 4)

	assert.Len(t, plan.AIDecisions[0].AIRules, maxSidecarAIRules, "at most 20 rules per entry")
	assert.Equal(t, "unbounded_scope", plan.AIDecisions[0].ApprovalReasonCode, "the reason code is kept beside the AI rules")

	d := plan.AIDecisions[1]
	require.Len(t, d.AIRules, 3, "an unknown kind is dropped")
	assert.Len(t, []rune(d.AIRules[0].Rule), maxSidecarAIRuleNameChars)
	assert.Len(t, []rune(d.AIRules[0].Reason), maxSidecarAIRuleReasonChars)
	require.NotNil(t, d.AIRules[0].Matched)
	assert.True(t, *d.AIRules[0].Matched)
	assert.JSONEq(t, `{"entities":["EMAIL_ADDRESS"],"keep_last":4}`, string(d.AIRules[1].Definition),
		"the definition is kept, NUL stripped")
	assert.Nil(t, d.AIRules[1].Matched, "matched is a guardrail's only")
	assert.Nil(t, d.AIRules[2].Definition, "a number jsonb would write in full is not stored")
	assert.Equal(t, "big number", d.AIRules[2].Reason)
	require.Len(t, d.AIRuleInvalid, 1)
	assert.Nil(t, d.AIRuleInvalid[0].Definition, "a definition past its bound is dropped")
	assert.Len(t, []rune(d.AIRuleInvalid[0].Error), maxSidecarAIRuleErrorChars)
	assert.Len(t, []rune(d.AIRulesError), maxSidecarAIRulesErrorChars)

	raw, err := json.Marshal(plan.AIDecisions[2])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"ai_rules":[]`, "a turn that applied nothing says so")
	raw, err = json.Marshal(plan.AIDecisions[3])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "ai_rules", "a value that is not a list stores nothing")
}

// The size counts the space jsonb writes after every colon and comma, nested
// ones included.
func TestSidecarAIDecisionSizeCountsNestedSeparators(t *testing.T) {
	f := false
	d := sidecarAIDecision{Seq: 1, RiskLevel: "low", AIRules: []sidecarAIRule{
		{Kind: "guardrail", Rule: "a:b,c", Reason: `"x", y`, Matched: &f},
		{Kind: "mask", Definition: json.RawMessage(`{"columns":["a","b"],"keep_last":4}`), Reason: "r"},
	}}
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	assert.Equal(t, len(raw)+jsonSeparators(raw)+2, sidecarAIDecisionSize(d))
	// seq, risk_level, ai_rules; kind, rule, reason, matched; kind,
	// definition, reason; columns, keep_last: 12 colons. Commas: 2 between
	// the top keys, 1 between the rules, 3 + 2 inside them, 1 in the list
	// and 1 in the definition: 10.
	assert.Equal(t, 22, jsonSeparators(raw))
}
