package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
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
		aiRulesLane(t, "appdb", `{"high":"block","medium":{"action":"warn","guardrails":{"rules":["multi","gone"]}}}`),
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
	assert.Equal(t, []string{"multi/no-drop", "multi/no-truncate"}, names, "only the named row, one entry per rule")
	assert.Empty(t, got.RuleLibrary.Mask, "no level uses mask, so no mask rule is served")
	assert.Equal(t, []string{"multi/no-drop", "multi/no-truncate", "gone"},
		got.Listeners[0].Analyzer.MediumRisk.Guardrails.Rules,
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
		aiRulesLane(t, "appdb", `{"low":{"action":"allow","mask":{"mode":"existing_and_new"}},`+
			`"medium":{"action":"warn","guardrails":{"rules":["no-delete"]}}}`),
	}}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	require.NotNil(t, got.RuleLibrary)
	require.Len(t, got.RuleLibrary.Guardrails, 1)
	assert.Equal(t, "no-delete", got.RuleLibrary.Guardrails[0].Name, "a one-rule row keeps the row's name")
	assert.JSONEq(t, `[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"},`+
		`{"name":"ssn","columns":["ssn"],"strategy":"hash"}]`, string(got.RuleLibrary.Mask),
		"every mask row, named as the row")
	assert.Nil(t, got.Listeners[0].Analyzer.LowRisk.Mask.Rules, "absent stays absent: every library rule")

	// Another lane selecting every guardrail widens the library to all of
	// them; a row no sidecar decodes is left out.
	stored.Listeners = append(stored.Listeners,
		aiRulesLane(t, "other", `{"high":{"action":"require_approval","approver":"ai_agent","guardrails":{}}}`))
	got, err = foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	var names []string
	for _, r := range got.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"multi/no-drop", "multi/no-truncate", "no-delete"}, names)
}

func TestFoldRuleLibraryLeavesADocumentWithoutAIRulesAlone(t *testing.T) {
	stored := daemon.Config{Listeners: []daemon.ListenerConfig{
		// Options on a block level apply nothing: the sidecar ignores them.
		aiRulesLane(t, "appdb", `{"high":{"action":"block","guardrails":{}},"medium":"warn"}`),
	}}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	assert.Nil(t, got.RuleLibrary)
}

func TestFoldRuleLibraryRefusesANameTheStoredLibraryHolds(t *testing.T) {
	stored := daemon.Config{
		RuleLibrary: &daemon.RuleLibrary{Mask: json.RawMessage(`[{"name":"ssn","columns":["ssn"]}]`)},
		Listeners: []daemon.ListenerConfig{
			aiRulesLane(t, "appdb", `{"medium":{"action":"warn","mask":{"rules":["emails"]}}}`),
		},
	}
	got, err := foldRuleLibrary(stored, aiRulesLibrary)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"ssn","columns":["ssn"]},{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"}]`,
		string(got.RuleLibrary.Mask), "the stored library is kept, the selected row added")

	stored.Listeners = []daemon.ListenerConfig{aiRulesLane(t, "appdb", `{"medium":{"action":"warn","mask":{}}}`)}
	_, err = foldRuleLibrary(stored, aiRulesLibrary)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"ssn" twice`)
}

func TestLibraryEntryNames(t *testing.T) {
	assert.Equal(t, []string{"row"}, libraryEntryNames("row", []string{"anything"}))
	assert.Equal(t, []string{"row/a", "row/b"}, libraryEntryNames("row", []string{"a", "b"}))
	assert.Equal(t, []string{"row/1", "row/2"}, libraryEntryNames("row", []string{"a", "a"}))
	assert.Equal(t, []string{"row/1", "row/2"}, libraryEntryNames("row", []string{"", "b"}))
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
		rated(2, map[string]string{analyzer.MetadataAIRules: string(manyRaw)}),
		rated(3, map[string]string{analyzer.MetadataAIRules: string(mixed),
			analyzer.MetadataAIRuleInvalid: string(invalid), analyzer.MetadataAIRulesError: huge}),
		rated(4, map[string]string{analyzer.MetadataAIRules: `[]`}),
		rated(5, map[string]string{analyzer.MetadataAIRules: `{"not":"a list"}`}),
	})
	require.NoError(t, err)
	require.Len(t, plan.AIDecisions, 4)

	assert.Len(t, plan.AIDecisions[0].AIRules, maxSidecarAIRules, "at most 20 rules per entry")

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
