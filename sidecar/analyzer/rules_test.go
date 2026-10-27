package analyzer_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// noDeletes matches deleteStatement; ordersOnly does not.
var (
	noDeletes = policy.Rule{Name: "no-deletes", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDelete}, Message: "deletes are not allowed here"}
	ordersOnly = policy.Rule{Name: "orders-only", Type: policy.MatchTable, Tables: []string{"orders"},
		Message: "orders are protected"}
)

// tagMask is a compiled masking rule that remembers its definition.
type tagMask struct{ def string }

func (m tagMask) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }
func (m tagMask) MaskCell(_ string, value []byte) ([]byte, []string, int) {
	return value, nil, 0
}

func compileMask(def json.RawMessage) (policy.ResponseMask, error) {
	var r struct {
		Entities []string `json:"entities"`
		Columns  []string `json:"columns"`
	}
	if err := json.Unmarshal(def, &r); err != nil {
		return nil, err
	}
	for _, e := range r.Entities {
		if e == "NOT_AN_ENTITY" {
			return nil, errors.New(`entity "NOT_AN_ENTITY" is not detected`)
		}
	}
	return tagMask{def: string(def)}, nil
}

var emailMask = analyzer.MaskRule{Name: "emails", Definition: json.RawMessage(`{"entities":["EMAIL_ADDRESS"]}`)}

// rulesEvaluator rates every delete medium and lets medium apply rules.
func rulesEvaluator(t *testing.T, p *handoffProvider, action analyzer.Action, lr analyzer.LevelRules,
	edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	p.level = analyzer.RiskMedium
	cfg := analyzer.Config{
		Rule:        "payments",
		Listener:    "payments",
		Provider:    p,
		Trigger:     deleteTrigger(),
		Actions:     analyzer.ActionMap{analyzer.RiskMedium: action},
		Rules:       map[analyzer.RiskLevel]analyzer.LevelRules{analyzer.RiskMedium: lr},
		CompileMask: compileMask,
		Timeout:     time.Second,
	}
	if edit != nil {
		edit(&cfg)
	}
	return fastWait(mustNew(t, cfg))
}

func bothExisting() analyzer.LevelRules {
	return analyzer.LevelRules{
		Guardrails: &analyzer.GuardrailOptions{Rules: []policy.Rule{noDeletes, ordersOnly}},
		Mask:       &analyzer.MaskOptions{Rules: []analyzer.MaskRule{emailMask}},
	}
}

func reply(calls ...analyzer.ToolCall) decideReply { return decideReply{calls: calls} }

func records(t *testing.T, v policy.Verdict) []map[string]any {
	t.Helper()
	raw, ok := v.Annotations[analyzer.MetadataAIRules]
	if !ok {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("ai_rules is not a JSON list: %v (%s)", err, raw)
	}
	return out
}

// The agent gets only the tools the level allows: apply tools with the enum
// of allowed names, create tools only in existing_and_new, and no_rule. The
// system prompt lists every allowed rule's definition, then the fixed suffix.
func TestTheRuleAgentGetsOnlyTheToolsTheLevelAllows(t *testing.T) {
	for name, tc := range map[string]struct {
		lr    analyzer.LevelRules
		tools []string
	}{
		"existing only": {bothExisting(), []string{"apply_guardrail", "apply_mask", "no_rule"}},
		"existing and new": {analyzer.LevelRules{
			Guardrails: &analyzer.GuardrailOptions{Mode: analyzer.RuleModeExistingAndNew, Rules: []policy.Rule{noDeletes}},
			Mask:       &analyzer.MaskOptions{Mode: analyzer.RuleModeExistingAndNew, Rules: []analyzer.MaskRule{emailMask}},
		}, []string{"apply_guardrail", "create_guardrail", "apply_mask", "create_mask", "no_rule"}},
		"guardrails only": {analyzer.LevelRules{
			Guardrails: &analyzer.GuardrailOptions{Rules: []policy.Rule{ordersOnly}},
		}, []string{"apply_guardrail", "no_rule"}},
		"create mask, no existing": {analyzer.LevelRules{
			Mask: &analyzer.MaskOptions{Mode: analyzer.RuleModeExistingAndNew},
		}, []string{"create_mask", "no_rule"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick("no_rule", "nothing to protect")}}
			ev := rulesEvaluator(t, p, analyzer.ActionWarn, tc.lr, nil)
			v := ev.EvaluateWith(deleteStatement(), withUser("ana@corp"))
			if v.Denied {
				t.Fatalf("denied: %s", v.Message)
			}
			asks := p.asks()
			if len(asks) != 1 {
				t.Fatalf("asked %d time(s)", len(asks))
			}
			if got := toolNames(asks[0].tools); !slices.Equal(got, tc.tools) {
				t.Fatalf("tools = %v, want %v", got, tc.tools)
			}
			for _, tool := range asks[0].tools {
				switch tool.Name {
				case "apply_guardrail":
					var want []string
					for _, r := range tc.lr.Guardrails.Rules {
						want = append(want, r.Name)
					}
					if got := tool.Input.Properties["rule"].Enum; !slices.Equal(got, want) {
						t.Errorf("apply_guardrail enum = %v, want %v", got, want)
					}
				case "apply_mask":
					if got := tool.Input.Properties["rule"].Enum; !slices.Equal(got, []string{"emails"}) {
						t.Errorf("apply_mask enum = %v", got)
					}
				case "create_guardrail":
					if p := tool.Input.Properties["words"]; p.Type != "array" || p.Items == nil {
						t.Errorf("words = %+v, want an array of strings", p)
					}
				}
				if !slices.Contains(tool.Input.Required, "reason") {
					t.Errorf("%s does not require a reason", tool.Name)
				}
			}
			if !strings.HasSuffix(asks[0].system, "\n\n"+analyzer.RulesSuffix) {
				t.Errorf("system prompt does not end with the fixed suffix:\n%s", asks[0].system)
			}
			var listed []string
			if tc.lr.Guardrails != nil {
				for _, r := range tc.lr.Guardrails.Rules {
					listed = append(listed, r.Name)
				}
			}
			if tc.lr.Mask != nil {
				for _, r := range tc.lr.Mask.Rules {
					listed = append(listed, r.Name)
				}
			}
			for _, name := range listed {
				if !strings.Contains(asks[0].system, "- "+name+": ") {
					t.Errorf("system prompt does not list %s:\n%s", name, asks[0].system)
				}
			}
			if !strings.Contains(asks[0].content, "User: ana@corp") {
				t.Errorf("content: %s", asks[0].content)
			}
			if got := v.Annotations[analyzer.MetadataAIRules]; got != "[]" {
				t.Errorf("ai_rules = %q, want []", got)
			}
		})
	}
}

// An applied guardrail that matches denies with its message; one that does
// not match is recorded as unmatched.
func TestAnAppliedGuardrailDeniesWithItsMessage(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(
		call("apply_guardrail", `{"rule":"orders-only","reason":"protects orders"}`),
		call("apply_guardrail", `{"rule":"no-deletes","reason":"destructive"}`),
	)}}
	ev := rulesEvaluator(t, p, analyzer.ActionAllow, bothExisting(), nil)
	ec := withUser("ana@corp")
	v := ev.EvaluateWith(deleteStatement(), ec)
	if !v.Denied || v.Message != "deletes are not allowed here" || v.Rule != "no-deletes" {
		t.Fatalf("verdict = %+v", v)
	}
	recs := records(t, v)
	if len(recs) != 2 || recs[0]["rule"] != "orders-only" || recs[0]["matched"] != false ||
		recs[1]["rule"] != "no-deletes" || recs[1]["matched"] != true || recs[1]["reason"] != "destructive" ||
		recs[1]["kind"] != "guardrail" {
		t.Errorf("ai_rules = %v", recs)
	}
	if len(ec.ResponseMasks) != 0 {
		t.Error("a denied statement carries a response mask")
	}
}

// A created guardrail compiles like a config rule and denies with its own
// message; the trail keeps its definition.
func TestACreatedGuardrailDeniesWithItsMessage(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(call("create_guardrail",
		`{"type":"table","tables":["users"],"message":"users are read only","reason":"protects users"}`))}}
	lr := analyzer.LevelRules{Guardrails: &analyzer.GuardrailOptions{Mode: analyzer.RuleModeExistingAndNew}}
	ev := rulesEvaluator(t, p, analyzer.ActionWarn, lr, nil)
	v := ev.EvaluateWith(deleteStatement(), withUser("ana@corp"))
	if !v.Denied || v.Message != "users are read only" {
		t.Fatalf("verdict = %+v", v)
	}
	recs := records(t, v)
	def, _ := json.Marshal(recs[0]["definition"])
	if len(recs) != 1 || recs[0]["rule"] != nil || !strings.Contains(string(def), `"tables":["users"]`) {
		t.Errorf("ai_rules = %v", recs)
	}
}

// An invalid created rule is dropped, recorded and reported; the valid rules
// beside it still apply, and none applies half-valid.
func TestAnInvalidCreatedRuleIsDroppedAndRecorded(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(
		call("create_guardrail", `{"type":"pattern_match","pattern_regex":"(","message":"x","reason":"bad regex"}`),
		call("create_mask", `{"entities":["NOT_AN_ENTITY"],"reason":"bad entity"}`),
		call("create_guardrail", `{"type":"ai_analysis","message":"x","reason":"not offered"}`),
		call("apply_mask", `{"rule":"emails","reason":"emails"}`),
	)}}
	lr := analyzer.LevelRules{
		Guardrails: &analyzer.GuardrailOptions{Mode: analyzer.RuleModeExistingAndNew},
		Mask:       &analyzer.MaskOptions{Mode: analyzer.RuleModeExistingAndNew, Rules: []analyzer.MaskRule{emailMask}},
	}
	var mu sync.Mutex
	var reported []string
	ev := rulesEvaluator(t, p, analyzer.ActionAllow, lr, func(c *analyzer.Config) {
		c.OnRuleInvalid = func(_ analyzer.RiskLevel, kind string, _ error) {
			mu.Lock()
			reported = append(reported, kind)
			mu.Unlock()
		}
	})
	ec := withUser("ana@corp")
	v := ev.EvaluateWith(deleteStatement(), ec)
	if v.Denied {
		t.Fatalf("denied: %s", v.Message)
	}
	var invalid []map[string]any
	if err := json.Unmarshal([]byte(v.Annotations[analyzer.MetadataAIRuleInvalid]), &invalid); err != nil || len(invalid) != 3 {
		t.Fatalf("ai_rule_invalid = %s", v.Annotations[analyzer.MetadataAIRuleInvalid])
	}
	for _, r := range invalid {
		if r["error"] == "" || r["reason"] == "" {
			t.Errorf("invalid record = %v", r)
		}
	}
	if !slices.Equal(reported, []string{"guardrail", "mask", "guardrail"}) {
		t.Errorf("reported %v", reported)
	}
	if recs := records(t, v); len(recs) != 1 || recs[0]["rule"] != "emails" {
		t.Errorf("ai_rules = %v", recs)
	}
	if len(ec.ResponseMasks) != 1 || ec.ResponseMasks[0].(tagMask).def != string(emailMask.Definition) {
		t.Errorf("response masks = %v", ec.ResponseMasks)
	}
}

// A created mask goes through the same compiler, under the lane's strategy
// limit: an ssh lane offers mask only.
func TestACreatedMaskKeepsTheStrategyLimit(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(
		call("create_mask", `{"entities":["EMAIL_ADDRESS"],"strategy":"redact","reason":"emails"}`),
		call("create_mask", `{"columns":["ssn"],"strategy":"mask","mask_char":"#","reason":"ssn"}`),
	)}}
	lr := analyzer.LevelRules{Mask: &analyzer.MaskOptions{Mode: analyzer.RuleModeExistingAndNew,
		Strategies: []string{"mask"}}}
	ev := rulesEvaluator(t, p, analyzer.ActionAllow, lr, nil)
	ec := withUser("ana@corp")
	v := ev.EvaluateWith(deleteStatement(), ec)
	if strategies := p.asks()[0].tools[0].Input.Properties["strategy"].Enum; !slices.Equal(strategies, []string{"mask"}) {
		t.Errorf("strategy enum = %v", strategies)
	}
	if !strings.Contains(v.Annotations[analyzer.MetadataAIRuleInvalid], "strategy") {
		t.Errorf("ai_rule_invalid = %s", v.Annotations[analyzer.MetadataAIRuleInvalid])
	}
	if len(ec.ResponseMasks) != 1 || !strings.Contains(ec.ResponseMasks[0].(tagMask).def, `"mask_char":35`) {
		t.Errorf("response masks = %v", ec.ResponseMasks)
	}
}

// With require_approval, an AI guardrail deny ends the statement and files no
// review.
func TestAnAIGuardrailDenyFilesNoReview(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(call("apply_guardrail", `{"rule":"no-deletes","reason":"destructive"}`))}}
	rev := &handoffReviewer{}
	ev := rulesEvaluator(t, p, analyzer.ActionRequireReview, bothExisting(), func(c *analyzer.Config) { c.Review = rev })
	v := policy.Chain{ev}.EvaluateWith(deleteStatement(), withUser("ana@corp"))
	if !v.Denied || v.Message != "deletes are not allowed here" {
		t.Fatalf("verdict = %+v", v)
	}
	if rev.filed() != 0 {
		t.Errorf("filed %d review(s)", rev.filed())
	}
}

// maskSeeingReviewer records whether the statement's response mask was set
// when the review was filed.
type maskSeeingReviewer struct {
	handoffReviewer
	ec      *policy.EvalContext
	atFile  int
	decided int
	p       *handoffProvider
}

func (r *maskSeeingReviewer) File(ctx context.Context, s string) (analyzer.ReviewResult, error) {
	r.atFile = len(r.ec.ResponseMasks)
	r.decided = len(r.p.asks())
	return r.handoffReviewer.File(ctx, s)
}

// With require_approval, an AI mask is attached before the hold files.
func TestAnAIMaskIsAttachedBeforeTheHold(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(call("apply_mask", `{"rule":"emails","reason":"emails"}`))}}
	ec := withUser("ana@corp")
	rev := &maskSeeingReviewer{ec: ec, p: p}
	ev := rulesEvaluator(t, p, analyzer.ActionRequireReview, bothExisting(), func(c *analyzer.Config) { c.Review = rev })
	policy.Chain{ev}.EvaluateWith(deleteStatement(), ec)
	if rev.filed() != 1 {
		t.Fatalf("filed %d review(s)", rev.filed())
	}
	if rev.decided != 1 || rev.atFile != 1 {
		t.Errorf("at filing: %d rule turn(s), %d mask(s); want both 1", rev.decided, rev.atFile)
	}
}

// The rule tools join the hand-off turn. block applies no rule; another
// action applies them.
func TestHandoffRulesApplyUnlessTheAgentBlocks(t *testing.T) {
	handoff := func(p *handoffProvider) *analyzer.Evaluator {
		return rulesEvaluator(t, p, analyzer.ActionHandoff, bothExisting(), func(c *analyzer.Config) {
			c.Handoffs = map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskMedium: {
				Allowed: []analyzer.Action{analyzer.ActionAllow, analyzer.ActionBlock}}}
		})
	}
	t.Run("block", func(t *testing.T) {
		p := &handoffProvider{replies: []decideReply{reply(
			call("block", `{"reason":"too wide"}`),
			call("apply_mask", `{"rule":"emails","reason":"emails"}`),
		)}}
		ec := withUser("ana@corp")
		v := handoff(p).EvaluateWith(deleteStatement(), ec)
		if !v.Denied || !strings.Contains(v.Message, "the hand-off agent blocked the statement") {
			t.Fatalf("verdict = %+v", v)
		}
		if _, ok := v.Annotations[analyzer.MetadataAIRules]; ok || len(ec.ResponseMasks) != 0 {
			t.Errorf("a blocked statement applied rules: %v %v", v.Annotations, ec.ResponseMasks)
		}
		if got := toolNames(p.asks()[0].tools); !slices.Equal(got, []string{"allow", "block", "apply_guardrail", "apply_mask"}) {
			t.Errorf("tools = %v", got)
		}
		if !strings.HasSuffix(p.asks()[0].system, analyzer.RulesSuffix) {
			t.Errorf("system prompt:\n%s", p.asks()[0].system)
		}
	})
	t.Run("allow", func(t *testing.T) {
		p := &handoffProvider{replies: []decideReply{reply(
			call("apply_mask", `{"rule":"emails","reason":"emails"}`),
			call("allow", `{"reason":"scoped"}`),
		)}}
		ec := withUser("ana@corp")
		v := handoff(p).EvaluateWith(deleteStatement(), ec)
		if v.Denied || len(ec.ResponseMasks) != 1 || v.Annotations[analyzer.MetadataHandoffDecision] != "allow" {
			t.Fatalf("verdict = %+v, masks %d", v, len(ec.ResponseMasks))
		}
	})
	t.Run("allow with a matching guardrail", func(t *testing.T) {
		p := &handoffProvider{replies: []decideReply{reply(
			call("allow", `{"reason":"scoped"}`),
			call("apply_guardrail", `{"rule":"no-deletes","reason":"destructive"}`),
		)}}
		v := handoff(p).EvaluateWith(deleteStatement(), withUser("ana@corp"))
		if !v.Denied || v.Message != "deletes are not allowed here" {
			t.Fatalf("verdict = %+v", v)
		}
	})
	t.Run("a malformed rule call keeps the action and applies nothing", func(t *testing.T) {
		p := &handoffProvider{replies: []decideReply{reply(
			call("allow", `{"reason":"scoped"}`),
			call("apply_guardrail", `{"rule":"not-in-the-enum","reason":"x"}`),
		)}}
		ec := withUser("ana@corp")
		v := handoff(p).EvaluateWith(deleteStatement(), ec)
		if v.Denied || v.Annotations[analyzer.MetadataAIRulesError] == "" || len(ec.ResponseMasks) != 0 {
			t.Fatalf("verdict = %+v", v)
		}
	})
}

// A failed rule turn applies no rule and keeps the level action.
func TestAFailedRuleTurnKeepsTheLevelAction(t *testing.T) {
	for name, tc := range map[string]struct {
		reply  decideReply
		edit   func(*analyzer.Config)
		stmt   inspect.Statement
		noCall bool
	}{
		"refusal":        {reply: decideReply{err: errors.New("refused")}},
		"no reason":      {reply: reply(call("apply_guardrail", `{"rule":"no-deletes"}`))},
		"unknown tool":   {reply: reply(call("drop_table", `{"reason":"x"}`))},
		"not in enum":    {reply: reply(call("apply_mask", `{"rule":"ssn","reason":"x"}`))},
		"not json":       {reply: reply(call("apply_mask", `nope`))},
		"too many calls": {reply: reply(slices.Repeat([]analyzer.ToolCall{call("apply_mask", `{"rule":"emails","reason":"x"}`)}, 17)...)},
		"budget spent": {
			reply: reply(call("apply_guardrail", `{"rule":"no-deletes","reason":"x"}`)),
			edit:  func(c *analyzer.Config) { c.MaxCalls = 1 }, noCall: true,
		},
		"truncated input": {
			reply: reply(call("apply_guardrail", `{"rule":"no-deletes","reason":"x"}`)),
			edit:  func(c *analyzer.Config) { c.MaxInputBytes = 16 }, noCall: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{tc.reply}}
			ev := rulesEvaluator(t, p, analyzer.ActionWarn, bothExisting(), tc.edit)
			ec := withUser("ana@corp")
			v := ev.EvaluateWith(deleteStatement(), ec)
			if v.Denied {
				t.Fatalf("denied: %s", v.Message)
			}
			if v.Annotations[analyzer.MetadataAction] != "warn" || v.Annotations[analyzer.MetadataAIRulesError] == "" {
				t.Errorf("annotations = %v", v.Annotations)
			}
			if _, ok := v.Annotations[analyzer.MetadataAIRules]; ok || len(ec.ResponseMasks) != 0 {
				t.Errorf("a failed turn applied rules")
			}
			if tc.noCall && len(p.asks()) != 0 {
				t.Errorf("asked %d time(s)", len(p.asks()))
			}
		})
	}
}

// The rule decision is cached with the classification under the exact
// content: the same statement and user reuse it, another user asks again.
func TestTheRuleDecisionIsCachedOnTheExactContent(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{reply(call("apply_mask", `{"rule":"emails","reason":"emails"}`))}}
	ev := rulesEvaluator(t, p, analyzer.ActionWarn, bothExisting(), withCache)
	for i, user := range []string{"ana@corp", "ana@corp", "bob@corp", "ana@corp"} {
		ec := withUser(user)
		v := ev.EvaluateWith(deleteStatement(), ec)
		if len(ec.ResponseMasks) != 1 || records(t, v)[0]["rule"] != "emails" {
			t.Fatalf("statement %d: masks %d, %v", i, len(ec.ResponseMasks), v.Annotations)
		}
	}
	if n := len(p.asks()); n != 2 {
		t.Errorf("rule turns = %d, want 2 (ana once, bob once)", n)
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("classifications = %d, want 1", n)
	}
}

// New refuses rule options on a level that applies none, and an existing
// guardrail that does not deny.
func TestNewRefusesRulesTheLevelCannotApply(t *testing.T) {
	for name, cfg := range map[string]struct {
		action analyzer.Action
		lr     analyzer.LevelRules
	}{
		"block level": {analyzer.ActionBlock, bothExisting()},
		"defer level": {analyzer.ActionDefer, bothExisting()},
		"defer rule": {analyzer.ActionWarn, analyzer.LevelRules{Guardrails: &analyzer.GuardrailOptions{
			Rules: []policy.Rule{{Name: "d", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpDelete},
				Action: policy.ActionDefer}}}}},
		"unknown mode":     {analyzer.ActionWarn, analyzer.LevelRules{Mask: &analyzer.MaskOptions{Mode: "all"}}},
		"no mask compiler": {analyzer.ActionWarn, analyzer.LevelRules{Mask: &analyzer.MaskOptions{Rules: []analyzer.MaskRule{emailMask}}}},
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{}
			p.level = analyzer.RiskMedium
			c := analyzer.Config{Provider: p, Trigger: deleteTrigger(),
				Actions:     analyzer.ActionMap{analyzer.RiskMedium: cfg.action},
				Rules:       map[analyzer.RiskLevel]analyzer.LevelRules{analyzer.RiskMedium: cfg.lr},
				CompileMask: compileMask}
			if name == "no mask compiler" {
				c.CompileMask = nil
			}
			if _, err := analyzer.New(c); err == nil {
				t.Fatal("built")
			}
		})
	}
}
