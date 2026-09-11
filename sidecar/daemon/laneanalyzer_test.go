package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// laneBlock is a minimal valid listener analyzer block.
func laneBlock() *LaneAnalyzerConfig {
	return &LaneAnalyzerConfig{
		Trigger: &policy.AITrigger{
			Operations: []inspect.Operation{inspect.OpDelete},
		},
		HighRisk: "block",
	}
}

// blockLane is a postgres lane carrying its own analyzer block.
func blockLane(la *LaneAnalyzerConfig) *Config {
	cfg := pgLane()
	cfg.Listeners[0].Analyzer = la
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	return cfg
}

// The analyzer is a per-lane component now: a listener declares it beside
// guardrails and mask, with no rule wrapper.
func TestLaneAnalyzerBlockIsAccepted(t *testing.T) {
	if err := blockLane(laneBlock()).Validate(); err != nil {
		t.Fatalf("a listener analyzer block was refused: %v", err)
	}
}

// The block needs the top-level section the same way the rule form did: the
// provider, the model and the credential live there.
func TestLaneAnalyzerWithoutTopLevelSectionIsRefused(t *testing.T) {
	cfg := blockLane(laneBlock())
	cfg.Analyzer = nil
	err := cfg.Validate()
	if err == nil {
		t.Fatal("an analyzer block with no top-level analyzer section was accepted")
	}
	if !strings.Contains(err.Error(), "analyzer") {
		t.Errorf("the error does not name the missing section: %v", err)
	}
}

// An empty trigger classifies nothing on an ungated lane, exactly as it did
// on the rule form, and is the correct spelling on a gated one.
func TestLaneAnalyzerTriggerIsRequiredUnlessGated(t *testing.T) {
	build := func(gate bool) error {
		cfg := blockLane(&LaneAnalyzerConfig{HighRisk: "block"})
		if gate {
			cfg.Listeners[0].OPA = &OPAConfig{
				URL: "http://opa:8181/v1/data/hoop", Gate: true,
			}
		}
		return cfg.Validate()
	}
	if err := build(false); err == nil {
		t.Error("an untriggered block on an ungated lane was accepted")
	}
	if err := build(true); err != nil {
		t.Errorf("an untriggered block on a gated lane was refused: %v", err)
	}
}

// A block naming no action for any risk level would allow every verdict,
// which looks like enforcement and is not.
func TestLaneAnalyzerWithoutAnyActionIsRefused(t *testing.T) {
	cfg := blockLane(&LaneAnalyzerConfig{
		Trigger: &policy.AITrigger{Operations: []inspect.Operation{inspect.OpDelete}},
	})
	err := cfg.Validate()
	if err == nil {
		t.Fatal("an analyzer block with no risk actions was accepted")
	}
	if !strings.Contains(err.Error(), "no action for any risk level") {
		t.Errorf("the error does not explain the allow-everything failure: %v", err)
	}
}

// The block refuses the same action values the rule form refuses, through
// the same shared check.
func TestLaneAnalyzerActionVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name, action, want string
	}{
		{"unknown", "explode", "unknown action"},
		{"require_review", "require_review", "hold a statement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			la := laneBlock()
			la.HighRisk = tc.action
			err := blockLane(la).Validate()
			if err == nil {
				t.Fatalf("action %q was accepted", tc.action)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// The numeric bounds a block overrides get the same negative refusal the
// top-level defaults get: a negative reads as "off" while looking set.
func TestLaneAnalyzerNegativeNumericsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		la   LaneAnalyzerConfig
	}{
		{"timeout_sec", LaneAnalyzerConfig{TimeoutSec: -1}},
		{"max_input_bytes", LaneAnalyzerConfig{MaxInputBytes: -1}},
		{"max_calls", LaneAnalyzerConfig{MaxCalls: -1}},
		{"cache.size", LaneAnalyzerConfig{Cache: &AnalyzerCacheConfig{Size: -1}}},
		{"cache.ttl_sec", LaneAnalyzerConfig{Cache: &AnalyzerCacheConfig{TTLSec: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			la := laneBlock()
			base := tc.la
			base.Trigger, base.HighRisk = la.Trigger, la.HighRisk
			err := blockLane(&base).Validate()
			if err == nil {
				t.Fatalf("a negative %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error %v does not name %s", err, tc.name)
			}
		})
	}
}

// An unknown send mode on the block is refused, not downgraded to raw.
func TestLaneAnalyzerUnknownSendModeIsRefused(t *testing.T) {
	la := laneBlock()
	la.Send = "plaintext"
	if err := blockLane(la).Validate(); err == nil {
		t.Fatal("an unknown send mode on the block was accepted")
	}
}

// The block's prompt beats the top-level default, which beats the built-in:
// the same precedence the rule form has, because both build through one
// path.
func TestLaneAnalyzerPromptPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		blockPrompt string
		cfgPrompt   string
		want        string
	}{
		{"block wins", "lane text", "cfg text", "lane text"},
		{"top-level default applies", "", "cfg text", "cfg text"},
		{"built-in when neither", "", "", "Risk levels:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			la := laneBlock()
			la.Prompt = tc.blockPrompt
			deps := &analyzerDeps{
				cfg:      &AnalyzerConfig{Provider: "stub", Model: "m", Prompt: tc.cfgPrompt},
				provider: stubAnalyzerProvider{},
			}
			ev, err := buildLaneAnalyzer("appdb", la, deps, true)
			if err != nil {
				t.Fatalf("buildLaneAnalyzer: %v", err)
			}
			got := ev.(*analyzer.Evaluator).SystemPrompt()
			if !strings.Contains(got, tc.want) {
				t.Errorf("prompt = %q, want it to contain %q", got, tc.want)
			}
			if !strings.Contains(got, "Never quote a literal value") {
				t.Error("the output contract was dropped")
			}
		})
	}
}

// The block overrides only what it names. max_calls is the observable one:
// a lane cap of 1 must beat an unbounded default, and the budget must key on
// the LANE name so a rebuilt block continues the running count.
func TestLaneAnalyzerOverridesAndBudgetKeyOnTheLane(t *testing.T) {
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: stubAnalyzerProvider{},
	}
	la := laneBlock()
	la.MaxCalls = 1
	stmt := inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM t",
		Operation: inspect.OpDelete,
	}

	gen1, err := buildLaneAnalyzer("appdb", la, deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}
	gen1.Evaluate(stmt) // spends the whole lane budget of 1

	gen2, err := buildLaneAnalyzer("appdb", la, deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}
	gen2.Evaluate(stmt)
	if got := gen2.(*analyzer.Evaluator).Stats().Calls; got != 1 {
		t.Fatalf("calls across generations = %d, want the lane budget of 1", got)
	}

	// A different lane is a different identity with its own purse.
	other, err := buildLaneAnalyzer("payments", la, deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}
	other.Evaluate(stmt)
	if got := other.(*analyzer.Evaluator).Stats().Calls; got != 1 {
		t.Fatalf("a second lane's budget = %d, want its own count of 1", got)
	}
}

// A deferring block makes the lane two-phase exactly as a deferring rule
// does: the decision that reads the finding has to run after the block that
// fills it.
func TestLaneAnalyzerDeferMakesTheLaneTwoPhase(t *testing.T) {
	la := laneBlock()
	la.HighRisk = "defer"
	s := laneStack{la: la, opa: &OPAConfig{URL: "http://opa:8181/v1/data/hoop"}}
	pol, err := buildLane(s, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	got := phases(t, pol)
	if len(got) != 1 || got[0] != policy.PhaseDecide {
		t.Fatalf("phases = %v, want exactly one decide-phase OPA after the analyzer", got)
	}
}

// A block that reads `high: defer` and behaves as `high: block` — the
// OPA-less degradation — has to say which one it did, so the lane carries
// the same startup note a deferring local rule gets.
func TestLaneAnalyzerDeferWithoutOPALeavesANote(t *testing.T) {
	la := laneBlock()
	la.HighRisk = "defer"
	lanes, err := Validate(blockLane(la), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var found bool
	for _, n := range lanes[0].Notes {
		if strings.Contains(n, "no opa.url") {
			found = true
		}
	}
	if !found {
		t.Errorf("no startup note explains the defer degradation: %v", lanes[0].Notes)
	}
}

// The rule form still loads, still works, and now says what replaced it.
func TestAIAnalysisRuleLoadsWithADeprecation(t *testing.T) {
	cfg := pgLane(aiRule("risky"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the deprecated rule form was refused: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var found bool
	for _, d := range cfg.Deprecations {
		if strings.Contains(d, "ai_analysis is deprecated") &&
			strings.Contains(d, `"analyzer" block`) {
			found = true
		}
	}
	if !found {
		t.Errorf("no deprecation names the analyzer block: %v", cfg.Deprecations)
	}
}

// Both spellings can serve one lane during a migration, each as its own
// evaluator; neither silently displaces the other.
func TestBlockAndRuleFormCoexist(t *testing.T) {
	cfg := blockLane(laneBlock())
	cfg.Listeners[0].Guardrails = &GuardrailsConfig{Rules: []policy.Rule{aiRule("risky")}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a migrating lane carrying both spellings was refused: %v", err)
	}
	lanes, err := Validate(cfg, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sum := lanes[0].Summary()
	if !strings.Contains(sum, "ai analyzer") || !strings.Contains(sum, "1 deprecated ai rule(s)") {
		t.Errorf("summary %q does not report the component and the leftover rule", sum)
	}
}

// -validate reports the analyzer as a component of the lane, never a rule
// count.
func TestValidateReportsTheAnalyzerAsAComponent(t *testing.T) {
	lanes, err := Validate(blockLane(laneBlock()), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !lanes[0].Analyzer {
		t.Error("LaneInfo does not report the analyzer block")
	}
	sum := lanes[0].Summary()
	if !strings.Contains(sum, "+ ai analyzer") {
		t.Errorf("summary %q does not report the analyzer component", sum)
	}
	if strings.Contains(sum, "ai rule") {
		t.Errorf("summary %q still reports a rule count for a block-only lane", sum)
	}
}

// The block on an HTTP lane needs body capture for the same reason the rule
// form did: a bodiless request is skipped, so the analyzer would never fire.
func TestHTTPLaneAnalyzerWithoutCaptureBodyIsRefused(t *testing.T) {
	cfg := &Config{
		Analyzer: &AnalyzerConfig{Provider: "stub", Model: "m"},
		Listeners: []ListenerConfig{{
			Name: "api", Protocol: "http", Listen: ":1", Upstream: "h:1",
			Analyzer: &LaneAnalyzerConfig{
				Trigger:  &policy.AITrigger{Resources: []string{"/orders/**"}},
				HighRisk: "block",
			},
		}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("an http lane analyzer with no capture_body was accepted")
	}
	if !strings.Contains(err.Error(), "capture_body") {
		t.Errorf("the error does not name capture_body: %v", err)
	}

	cfg.Listeners[0].HTTP = &HTTPCodecConfig{CaptureBody: true}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the same lane with capture_body was refused: %v", err)
	}
}

// Editing only a lane's analyzer block must swap on a hot reload rather
// than demand a restart: the block builds evaluators, not sockets.
func TestLaneAnalyzerBlockIsInsideTheReloadBoundary(t *testing.T) {
	before := blockLane(laneBlock())
	after := blockLane(laneBlock())
	after.Listeners[0].Analyzer.HighRisk = "warn"

	b1, err := nonRuleDoc(before)
	if err != nil {
		t.Fatalf("nonRuleDoc: %v", err)
	}
	b2, err := nonRuleDoc(after)
	if err != nil {
		t.Fatalf("nonRuleDoc: %v", err)
	}
	if string(b1) != string(b2) {
		t.Error("an analyzer block edit changed the restart-guarded document")
	}

	d1, err := laneRuleDoc(before, before.Listeners[0])
	if err != nil {
		t.Fatalf("laneRuleDoc: %v", err)
	}
	d2, err := laneRuleDoc(after, after.Listeners[0])
	if err != nil {
		t.Fatalf("laneRuleDoc: %v", err)
	}
	if string(d1) == string(d2) {
		t.Error("an analyzer block edit did not change the lane's rule document")
	}
}

// recordingProvider captures what actually leaves the process, which is the
// only honest way to test redaction: an assertion on the redactor alone
// cannot notice a caller that skipped it.
type recordingProvider struct {
	mu    sync.Mutex
	texts []string
}

func (r *recordingProvider) Name() string { return "recording" }

func (r *recordingProvider) Classify(_ context.Context, _ string, text string) (*analyzer.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	return &analyzer.Result{RiskLevel: analyzer.RiskLow}, nil
}

func (r *recordingProvider) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...)
}

func deleteStmt(text string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      text,
		Operation: inspect.OpDelete,
	}
}

// A block naming ONE cache field keeps the other's inherited value, like
// every other override. Replacing the struct wholesale would zero the
// unnamed field, and a zero on either side disables caching.
func TestLaneCacheOverrideMergesFieldWise(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cache *AnalyzerCacheConfig
	}{
		{"ttl only keeps the inherited size", &AnalyzerCacheConfig{TTLSec: 60}},
		{"size only keeps the inherited ttl", &AnalyzerCacheConfig{Size: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingProvider{}
			deps := &analyzerDeps{
				cfg: &AnalyzerConfig{Provider: "stub", Model: "m",
					Cache: AnalyzerCacheConfig{Size: 128, TTLSec: 900}},
				provider: rec,
			}
			la := laneBlock()
			la.Cache = tc.cache
			ev, err := buildLaneAnalyzer("appdb", la, deps, false)
			if err != nil {
				t.Fatalf("buildLaneAnalyzer: %v", err)
			}
			ev.Evaluate(deleteStmt("DELETE FROM t WHERE id = 1"))
			ev.Evaluate(deleteStmt("DELETE FROM t WHERE id = 1"))
			if calls := len(rec.sent()); calls != 1 {
				t.Fatalf("provider calls = %d, want 1: a partial cache override "+
					"disabled the cache instead of merging", calls)
			}
		})
	}
}

// A lane's block and a DEPRECATED rule that happens to carry the lane's
// name must not share a purse: the two forms draw names from different
// namespaces, and coexistence is supported during migration.
func TestBlockAndRuleBudgetsDoNotCollideOnOneName(t *testing.T) {
	rec := &recordingProvider{}
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m", MaxCalls: 1},
		provider: rec,
	}

	block, err := buildLaneAnalyzer("appdb", laneBlock(), deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}
	rules, err := buildAnalyzerEvaluators([]policy.Rule{aiRule("appdb")}, deps, false)
	if err != nil {
		t.Fatalf("buildAnalyzerEvaluators: %v", err)
	}

	block.Evaluate(deleteStmt("DELETE FROM a"))    // spends the lane's budget of 1
	rules[0].Evaluate(deleteStmt("DELETE FROM b")) // must spend the RULE's own budget

	if calls := len(rec.sent()); calls != 2 {
		t.Fatalf("provider calls = %d, want 2: the rule named after the lane "+
			"paid from the lane's purse", calls)
	}
}

// send: redacted must never transmit a detected value. The provider sees
// the entity class where the value stood, and never the value.
func TestRedactedSendTransmitsNoValues(t *testing.T) {
	const pan = "4111111111111111"
	rec := &recordingProvider{}
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m", Send: SendRedacted},
		provider: rec,
		det:      stubPlugin{entities: []string{"CREDIT_CARD"}, find: pan},
	}
	ev, err := buildLaneAnalyzer("appdb", laneBlock(), deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}

	ev.Evaluate(deleteStmt("DELETE FROM cards WHERE pan = '" + pan + "'"))

	sent := rec.sent()
	if len(sent) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(sent))
	}
	if strings.Contains(sent[0], pan) {
		t.Fatalf("the detected value left the process:\n%s", sent[0])
	}
	if !strings.Contains(sent[0], "<CREDIT_CARD>") {
		t.Errorf("the entity class did not replace the value:\n%s", sent[0])
	}
}

// A lane overriding send to redacted or refuse in a build with no detector
// would get a nil redactor and transmit raw text under a name that promises
// otherwise. The build refuses instead, exactly like the top-level check.
func TestLanePrivacySendWithoutDetectorIsRefused(t *testing.T) {
	for _, mode := range []SendMode{SendRedacted, SendRefuse} {
		t.Run(string(mode), func(t *testing.T) {
			deps := &analyzerDeps{
				cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"}, // send: raw default
				provider: stubAnalyzerProvider{},
			}
			la := laneBlock()
			la.Send = mode
			_, err := buildLaneAnalyzer("appdb", la, deps, false)
			if err == nil || !strings.Contains(err.Error(), "detector") {
				t.Fatalf("a %s override with no detector was accepted: %v", mode, err)
			}

			// The same refusal through the front door.
			cfg := blockLane(la)
			if _, verr := Validate(cfg, nil); verr == nil {
				t.Fatalf("Validate accepted a %s lane with no detector", mode)
			}
		})
	}
}

// send: refuse runs on EVERY statement, cache hit or not. The cache keys on
// the statement's shape, so a clean statement must not open a hole for a
// sensitive literal with the same shape.
func TestRefuseRunsOnCacheHits(t *testing.T) {
	const pan = "4111111111111111"
	rec := &recordingProvider{}
	deps := &analyzerDeps{
		cfg: &AnalyzerConfig{Provider: "stub", Model: "m", Send: SendRefuse,
			Cache: AnalyzerCacheConfig{Size: 16, TTLSec: 900}},
		provider: rec,
		det:      stubPlugin{entities: []string{"CREDIT_CARD"}, find: pan},
	}
	ev, err := buildLaneAnalyzer("appdb", laneBlock(), deps, false)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}

	// A clean statement seeds the cache for this SQL shape.
	if v := ev.Evaluate(deleteStmt("DELETE FROM cards WHERE pan = '1'")); v.Denied {
		t.Fatalf("the clean statement was denied: %+v", v)
	}
	// The same shape carrying the sensitive literal must be refused
	// locally, not served the cached verdict.
	v := ev.Evaluate(deleteStmt("DELETE FROM cards WHERE pan = '" + pan + "'"))
	if !v.Denied {
		t.Fatal("a sensitive literal rode a cached shape past send: refuse")
	}
	if calls := len(rec.sent()); calls != 1 {
		t.Fatalf("provider calls = %d, want 1: the refusal must not cost a call", calls)
	}
	for _, sent := range rec.sent() {
		if strings.Contains(sent, pan) {
			t.Fatalf("the detected value left the process:\n%s", sent)
		}
	}
}
