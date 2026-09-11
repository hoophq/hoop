package daemon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// One listener-scope ai_analysis rule is the clean case: it becomes the
// lane's block, field for field, and the rule disappears.
func TestMigrateMovesAListenerRuleOntoTheBlock(t *testing.T) {
	r := aiRule("risky")
	r.MediumRisk = "warn"
	r.Prompt = "judge the ledger"
	r.Message = "refused"
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	notes := cfg.MigrateDeprecated()

	lc := cfg.Listeners[0]
	if lc.Analyzer == nil {
		t.Fatal("no analyzer block was created")
	}
	if lc.Analyzer.HighRisk != "block" || lc.Analyzer.MediumRisk != "warn" ||
		lc.Analyzer.Prompt != "judge the ledger" || lc.Analyzer.Message != "refused" ||
		lc.Analyzer.Trigger.IsZero() {
		t.Errorf("block lost fields: %+v", lc.Analyzer)
	}
	// The rule was the guardrails block's only content, so the block goes
	// too rather than leaving an empty stub.
	if lc.Guardrails != nil {
		t.Errorf("an emptied guardrails block was kept: %+v", lc.Guardrails)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "ai_rule") {
		t.Errorf("the notes do not warn about the identity change: %v", notes)
	}
}

// Local rules survive the move untouched, in order.
func TestMigrateKeepsLocalRules(t *testing.T) {
	local := policy.Rule{Name: "no-drop", Type: policy.MatchDenyWords, Words: []string{"drop"}}
	cfg := pgLane(local, aiRule("risky"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	cfg.MigrateDeprecated()

	lc := cfg.Listeners[0]
	if lc.Guardrails == nil || len(lc.Guardrails.Rules) != 1 || lc.Guardrails.Rules[0].Name != "no-drop" {
		t.Fatalf("local rules did not survive: %+v", lc.Guardrails)
	}
	if lc.Analyzer == nil {
		t.Fatal("the ai rule did not become a block")
	}
}

// Two rules on one lane cannot become one block, so the tool leaves them and
// says why instead of guessing which one wins.
func TestMigrateLeavesAMultiRuleLaneAlone(t *testing.T) {
	cfg := pgLane(aiRule("writes"), aiRule("reads"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	notes := cfg.MigrateDeprecated()

	lc := cfg.Listeners[0]
	if lc.Analyzer != nil {
		t.Fatal("a lossy move was made")
	}
	if len(lc.Guardrails.Rules) != 2 {
		t.Fatalf("rules were dropped: %+v", lc.Guardrails.Rules)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "2 ai_analysis rules left in place") {
		t.Errorf("notes = %v", notes)
	}
}

// A lane that already has a block keeps its rules: the block is occupied and
// a second trigger cannot be absorbed.
func TestMigrateLeavesAnOccupiedLaneAlone(t *testing.T) {
	cfg := pgLane(aiRule("risky"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	la := LaneAnalyzerConfig{HighRisk: "block"}
	cfg.Listeners[0].Analyzer = &la

	notes := cfg.MigrateDeprecated()

	if len(cfg.Listeners[0].Guardrails.Rules) != 1 {
		t.Fatal("the rule was moved onto an occupied lane")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "already has") {
		t.Errorf("notes = %v", notes)
	}
}

// A single top-level rule reached every lane through concatenation, so the
// faithful move is a block on every listener.
func TestMigrateCopiesATopLevelRuleToEveryFreeLane(t *testing.T) {
	cfg := &Config{
		Analyzer:   &AnalyzerConfig{Provider: "stub", Model: "m"},
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{aiRule("risky")}},
		Listeners: []ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "payments", Protocol: "postgres", Listen: ":2", Upstream: "h:2"},
		},
	}

	notes := cfg.MigrateDeprecated()

	if cfg.Guardrails != nil {
		t.Errorf("the emptied top-level guardrails block was kept: %+v", cfg.Guardrails)
	}
	for _, lc := range cfg.Listeners {
		if lc.Analyzer == nil || lc.Analyzer.HighRisk != "block" {
			t.Fatalf("%s did not receive the block: %+v", lc.Name, lc.Analyzer)
		}
	}
	// Copies must not share memory: one lane's later edit is not the
	// other's.
	if cfg.Listeners[0].Analyzer == cfg.Listeners[1].Analyzer {
		t.Fatal("two lanes share one block pointer")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "own max_calls budget") {
		t.Errorf("notes = %v", notes)
	}
}

// A top-level rule with one occupied lane stays put: moving it to the free
// lanes only would change which lanes run it.
func TestMigrateLeavesATopLevelRuleWhenALaneIsOccupied(t *testing.T) {
	la := LaneAnalyzerConfig{HighRisk: "block"}
	cfg := &Config{
		Analyzer:   &AnalyzerConfig{Provider: "stub", Model: "m"},
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{aiRule("risky")}},
		Listeners: []ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "payments", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				Analyzer: &la},
		},
	}

	notes := cfg.MigrateDeprecated()

	if cfg.Guardrails == nil || len(cfg.Guardrails.Rules) != 1 {
		t.Fatal("the top-level rule was moved despite an occupied lane")
	}
	if cfg.Listeners[0].Analyzer != nil {
		t.Fatal("a partial move reached the free lane")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "payments") {
		t.Errorf("the note does not name the occupied lane: %v", notes)
	}
}

// A config with nothing deprecated migrates to itself: no notes, no moves.
func TestMigrateIsANoOpOnACanonicalConfig(t *testing.T) {
	cfg := blockLane(laneBlock())
	before, _ := json.Marshal(cfg)

	notes := cfg.MigrateDeprecated()

	after, _ := json.Marshal(cfg)
	if !bytes.Equal(before, after) {
		t.Error("a canonical config was rewritten")
	}
	if len(notes) != 0 {
		t.Errorf("notes on a canonical config: %v", notes)
	}
}

// The rendered document must load, carry no zero-value noise, and mean
// exactly what the in-memory config means.
func TestRenderMigratedIsCleanAndLossless(t *testing.T) {
	cfg := pgLane(aiRule("risky"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	cfg.MigrateDeprecated()

	doc, err := RenderMigrated(cfg)
	if err != nil {
		t.Fatalf("RenderMigrated: %v", err)
	}

	reloaded, err := LoadConfigBytes(doc)
	if err != nil {
		t.Fatalf("the rendered document does not load: %v\n%s", err, doc)
	}
	if len(reloaded.Deprecations) != 0 {
		t.Errorf("the rendered document still warns: %v", reloaded.Deprecations)
	}
	if reloaded.Listeners[0].Analyzer == nil {
		t.Fatal("the block did not survive the render")
	}

	for _, noise := range []string{`"network"`, `"upstream_tls"`, `"idle_timeout_sec"`, `"log_level"`} {
		if bytes.Contains(doc, []byte(noise)) {
			t.Errorf("zero-value noise %s in the render:\n%s", noise, doc)
		}
	}
}

// The prune must not eat the spellings whose presence is the meaning: an
// opt-out opa block, an empty rules override, an explicit fail_open: false.
func TestRenderMigratedKeepsMeaningfulEmptiness(t *testing.T) {
	off := false
	cfg := &Config{
		Analyzer: &AnalyzerConfig{Provider: "stub", Model: "m", FailOpen: &off},
		OPA:      &OPAConfig{URL: "http://opa:8181/v1/data/hoop"},
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{
			{Name: "no-drop", Type: policy.MatchDenyWords, Words: []string{"drop"}},
		}},
		Listeners: []ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			OPA:        &OPAConfig{},
			Guardrails: &GuardrailsConfig{Rules: []policy.Rule{}},
		}},
	}

	doc, err := RenderMigrated(cfg)
	if err != nil {
		t.Fatalf("RenderMigrated: %v", err)
	}
	reloaded, err := LoadConfigBytes(doc)
	if err != nil {
		t.Fatalf("the rendered document does not load: %v\n%s", err, doc)
	}

	if gc, opa, _ := reloaded.resolve(reloaded.Listeners[0]); opa.enabled() {
		t.Errorf("the lane's opa opt-out was pruned away:\n%s", doc)
	} else if len(gc.Rules) != 0 {
		t.Errorf("the lane's empty rules override was pruned away:\n%s", doc)
	}
	if reloaded.Analyzer.failOpen() {
		t.Errorf("the explicit fail_open: false was pruned away:\n%s", doc)
	}
}

// WriteMigrated is the whole command: document out, notes and the diff
// warning on the report, and the leftover count when a lane could not move.
func TestWriteMigratedReportsLeftovers(t *testing.T) {
	cfg := pgLane(aiRule("writes"), aiRule("reads"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	var out, report bytes.Buffer
	if err := WriteMigrated(cfg, false, &out, &report); err != nil {
		t.Fatalf("WriteMigrated: %v", err)
	}

	if _, err := LoadConfigBytes(out.Bytes()); err != nil {
		t.Fatalf("the emitted document does not load: %v", err)
	}
	r := report.String()
	if !strings.Contains(r, "left in place") {
		t.Errorf("the report does not name the unmovable rules: %s", r)
	}
	if !strings.Contains(r, "still uses 2 deprecated field(s)") {
		t.Errorf("the report does not count the leftovers: %s", r)
	}
	if !strings.Contains(r, "comments and key order") {
		t.Errorf("the report does not warn about the rebuild: %s", r)
	}
}
