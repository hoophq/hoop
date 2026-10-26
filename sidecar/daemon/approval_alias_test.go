package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// approvalLane is holdingLane with the analyzer block edited.
func approvalLane(edit func(*LaneAnalyzerConfig)) *Config {
	cfg := holdingLane()
	edit(cfg.Listeners[0].Analyzer)
	return cfg
}

// approval_mode is an alias of review_mode: alone it is copied, written twice
// with one value it loads, written twice with two values it is refused. After
// normalize nothing reads the alias, and no deprecation is recorded.
func TestApprovalModeFoldsOntoReviewMode(t *testing.T) {
	for name, tc := range map[string]struct {
		approval, review analyzer.ReviewMode
		want             analyzer.ReviewMode
		conflict         bool
	}{
		"alias only":     {approval: "return", want: "return"},
		"both equal":     {approval: "return", review: "return", want: "return"},
		"both differ":    {approval: "return", review: "hold", conflict: true},
		"canonical only": {review: "return", want: "return"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := approvalLane(func(la *LaneAnalyzerConfig) {
				la.ApprovalMode, la.ReviewMode = tc.approval, tc.review
			})
			err := cfg.normalize()
			if tc.conflict {
				if err == nil || !strings.Contains(err.Error(), "approval_mode or review_mode, not both") {
					t.Fatalf("normalize = %v, want a conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			la := cfg.Listeners[0].Analyzer
			if la.ReviewMode != tc.want || la.ApprovalMode != "" {
				t.Errorf("review_mode=%q approval_mode=%q, want %q and empty", la.ReviewMode, la.ApprovalMode, tc.want)
			}
			if len(cfg.Deprecations) != 0 {
				t.Errorf("an alias is not a deprecation: %v", cfg.Deprecations)
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

// The same through the decoder an operator's file goes through.
func TestAnApprovalSpelledConfigLoads(t *testing.T) {
	doc := `{"analyzer":{"provider":"stub","model":"m"},"listeners":[{"name":"appdb","protocol":"postgres",
		"listen":":1","upstream":"h:1","analyzer":{"trigger":{"operations":["delete"]},
		"high":"require_approval","approval_rule":"payments-approvers","approval_mode":"return"}}]}`
	var cfg Config
	if err := json.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk.Action != "require_review" || la.ReviewMode != "return" || la.ApprovalMode != "" {
		t.Errorf("block is high=%q review_mode=%q approval_mode=%q, want require_review, return and empty",
			la.HighRisk.Action, la.ReviewMode, la.ApprovalMode)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// require_approval folds onto require_review on every risk level, on the
// block and on the deprecated rule form, so analyzerHolds and
// refuseRuleFormHold read one value.
func TestRequireApprovalFoldsOntoRequireReview(t *testing.T) {
	cfg := approvalLane(func(la *LaneAnalyzerConfig) {
		la.HighRisk.Action, la.MediumRisk.Action, la.LowRisk.Action = "require_approval", "require_approval", "allow"
	})
	rule := aiRule("risky")
	rule.MediumRisk = "require_approval"
	cfg.Listeners[0].Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule}}
	cfg.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule}}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk.Action != "require_review" || la.MediumRisk.Action != "require_review" || la.LowRisk.Action != "allow" {
		t.Errorf("block levels are %q/%q/%q", la.HighRisk.Action, la.MediumRisk.Action, la.LowRisk.Action)
	}
	for _, rules := range [][]policy.Rule{cfg.Listeners[0].Guardrails.Rules, cfg.Guardrails.Rules} {
		if rules[0].MediumRisk != "require_review" {
			t.Errorf("rule medium is %q, want require_review", rules[0].MediumRisk)
		}
	}
	if got := refuseRuleFormHold(rule.HighRisk, rule.MediumRisk, rule.LowRisk, "x"); len(got) == 0 {
		t.Error("the rule form accepted require_approval, which it cannot honor")
	}
}

// Read without normalize, as the plane's own checks read a stored block, the
// alias still holds: no check can miss a hold over its spelling.
func TestRequireApprovalHoldsWithoutNormalize(t *testing.T) {
	la := laneBlock()
	la.HighRisk = RiskLevel{Action: "require_approval"}
	if !analyzerHolds(la) {
		t.Error("analyzerHolds missed require_approval")
	}
	problems := ValidateLaneAnalyzerBlock(la, "appdb")
	if len(problems) != 1 || !strings.Contains(problems[0], "names no approval_rule") {
		t.Errorf("problems %v, want only the missing approval_rule", problems)
	}
	m, err := actionMap(*la, false)
	if err != nil || m[analyzer.RiskHigh] != analyzer.ActionRequireReview {
		t.Errorf("actionMap = %v, %v, want require_review", m, err)
	}
}

// The action list an operator reads names both spellings.
func TestAnUnknownActionNamesBothHoldSpellings(t *testing.T) {
	la := laneBlock()
	la.HighRisk = RiskLevel{Action: "hold_please"}
	problems := ValidateLaneAnalyzerBlock(la, "appdb")
	if len(problems) == 0 || !strings.Contains(problems[0],
		"(allow, warn, block, defer or require_review, also spelled require_approval)") {
		t.Errorf("problems %v do not list both spellings", problems)
	}
}

// The plane serves the canonical spellings, so a build that predates the
// aliases still decodes what an admin wrote with them.
func TestServedFormServesTheCanonicalSpellings(t *testing.T) {
	stored := approvalLane(func(la *LaneAnalyzerConfig) {
		la.HighRisk.Action, la.ApprovalMode = "require_approval", "return"
	})
	rule := aiRule("risky")
	rule.HighRisk = "require_approval"
	stored.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule}}

	served := ServedForm(*stored)
	la := served.Listeners[0].Analyzer
	if la.HighRisk.Action != "require_review" || la.ReviewMode != "return" || la.ApprovalMode != "" {
		t.Errorf("served block is high=%q review_mode=%q approval_mode=%q", la.HighRisk.Action, la.ReviewMode, la.ApprovalMode)
	}
	if served.Guardrails.Rules[0].HighRisk != "require_review" {
		t.Errorf("served rule high is %q", served.Guardrails.Rules[0].HighRisk)
	}
	if stored.Listeners[0].Analyzer.ApprovalMode != "return" || stored.Guardrails.Rules[0].HighRisk != "require_approval" {
		t.Error("ServedForm changed the stored document")
	}
	// Served to a build that knows neither alias.
	if err := CheckServable(*stored, Handshake{Capabilities: []string{CapabilityReviewMode}, Version: "1.196.0"}); err != nil {
		t.Errorf("a build without approval_mode was refused the canonical form: %v", err)
	}

	// approval_mode hold folds to the default, which serves no key at all.
	held := ServedForm(*approvalLane(func(la *LaneAnalyzerConfig) { la.ApprovalMode = "hold" }))
	if la := held.Listeners[0].Analyzer; la.ReviewMode != "" || la.ApprovalMode != "" {
		t.Errorf("a hold alias served review_mode=%q approval_mode=%q", la.ReviewMode, la.ApprovalMode)
	}

	// Two different answers are served as written, for the sidecar to refuse.
	both := ServedForm(*approvalLane(func(la *LaneAnalyzerConfig) { la.ApprovalMode, la.ReviewMode = "return", "hold" }))
	if la := both.Listeners[0].Analyzer; la.ApprovalMode != "return" || la.ReviewMode != "hold" {
		t.Errorf("a conflict was resolved in the served form: %+v", la)
	}
}

// The handshake reports approval_mode, generated from its cap tag.
func TestTheHeaderReportsApprovalMode(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApprovalMode) {
		t.Errorf("the header lacks %q: %v", CapabilityApprovalMode, SidecarCapabilities())
	}
}

// A grpc lane that holds carries the approval header onto request messages,
// beside the review header when the client sent both.
func TestAHoldingGRPCLaneCarriesTheApprovalModeOntoRequestMessages(t *testing.T) {
	allow := grpcMetadataAllowlist(GRPCCodecConfig{}, analyzerHolds(holdingBlock()))
	req := httptest.NewRequest(http.MethodPost, "/ledger.v1.Ledger/Transfer", nil)
	req.Header.Set("X-Hoop-Approval-Mode", "return")
	req.Header.Set("X-Hoop-Review-Mode", "return")
	stmts := newLaneStatements(req, "ledger.v1.Ledger", "Transfer", allow, inspect.GRPC, nil)

	msg := stmts.message(inspect.FromClient, `{"amount":"100"}`, false, 1)
	if got := msg.HTTP.Headers; len(got) != 2 || got[analyzer.HeaderApprovalMode] != "return" ||
		got[analyzer.HeaderReviewMode] != "return" {
		t.Errorf("request message headers are %v, want both mode headers", got)
	}
}

// An http lane captures the approval header as it captures the review one.
func TestAnHTTPLaneCapturesTheApprovalModeHeader(t *testing.T) {
	f := laneCodecFactory(ListenerConfig{Protocol: "http"}, nil)
	stmts, _, err := f().Decode(inspect.FromClient, []byte(
		"POST /x HTTP/1.1\r\nHost: h\r\nX-Hoop-Approval-Mode: return\r\nContent-Length: 0\r\n\r\n"))
	if err != nil || len(stmts) != 1 {
		t.Fatalf("decode: %v, %d statements", err, len(stmts))
	}
	if got := stmts[0].HTTP.Headers[analyzer.HeaderApprovalMode]; got != "return" {
		t.Errorf("approval header is %q, want return", got)
	}
}

// Canonicalize is the fold the control plane runs on a block it decoded
// without normalize.
func TestCanonicalizeFoldsOneBlock(t *testing.T) {
	la := LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval"}, LowRisk: RiskLevel{Action: "allow"}, ApprovalMode: "return"}
	if err := la.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if la.HighRisk.Action != string(analyzer.ActionRequireReview) || la.LowRisk.Action != "allow" ||
		la.ReviewMode != "return" || la.ApprovalMode != "" {
		t.Errorf("folded %+v", la)
	}

	same := LaneAnalyzerConfig{ApprovalMode: "return", ReviewMode: "return"}
	if err := same.Canonicalize(); err != nil || same.ReviewMode != "return" || same.ApprovalMode != "" {
		t.Errorf("the same mode twice: %+v, %v", same, err)
	}

	both := LaneAnalyzerConfig{ApprovalMode: "return", ReviewMode: "hold"}
	if err := both.Canonicalize(); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("a conflict folded: %v", err)
	}
}

// CanonicalizeApprovalSpellings folds a whole document, naming the listener
// of a conflict.
func TestCanonicalizeApprovalSpellingsFoldsADocument(t *testing.T) {
	cfg := Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{{Name: "r", HighRisk: "require_approval"}}},
		Listeners: []ListenerConfig{{
			Name:     "agents",
			Analyzer: &LaneAnalyzerConfig{MediumRisk: RiskLevel{Action: "require_approval"}, ApprovalMode: "hold"},
		}},
	}
	if err := CanonicalizeApprovalSpellings(&cfg); err != nil {
		t.Fatal(err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.MediumRisk.Action != string(analyzer.ActionRequireReview) || la.ReviewMode != "hold" || la.ApprovalMode != "" {
		t.Errorf("folded block %+v", la)
	}
	if got := cfg.Guardrails.Rules[0].HighRisk; got != string(analyzer.ActionRequireReview) {
		t.Errorf("folded rule action %q", got)
	}

	cfg.Listeners[0].Analyzer = &LaneAnalyzerConfig{ApprovalMode: "return", ReviewMode: "hold"}
	err := CanonicalizeApprovalSpellings(&cfg)
	if err == nil || !strings.Contains(err.Error(), "agents: analyzer: set approval_mode or review_mode, not both") {
		t.Errorf("a conflict folded: %v", err)
	}
}

// The plane validates a block that never went through normalize. An
// approval_mode no sidecar accepts, or one that disagrees with review_mode,
// is refused there instead of by every sidecar the block reaches.
func TestValidateLaneAnalyzerBlockJudgesTheApprovalSpellings(t *testing.T) {
	hold := func(edit func(*LaneAnalyzerConfig)) *LaneAnalyzerConfig {
		la := &LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval"}, ApprovalRule: "r"}
		edit(la)
		return la
	}
	for name, tc := range map[string]struct {
		block *LaneAnalyzerConfig
		want  string
	}{
		"unknown approval_mode": {hold(func(la *LaneAnalyzerConfig) { la.ApprovalMode = "bogus" }), "bogus"},
		"two modes":             {hold(func(la *LaneAnalyzerConfig) { la.ApprovalMode, la.ReviewMode = "return", "hold" }), "not both"},
		"mode with no hold": {&LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "block"}, ApprovalMode: "return"},
			"nothing on this lane would hold one"},
	} {
		problems := strings.Join(ValidateLaneAnalyzerBlock(tc.block, "rule"), "; ")
		if !strings.Contains(problems, tc.want) {
			t.Errorf("%s: problems %q, want one naming %q", name, problems, tc.want)
		}
	}

	ok := hold(func(la *LaneAnalyzerConfig) { la.ApprovalMode = "return" })
	if problems := ValidateLaneAnalyzerBlock(ok, "rule"); len(problems) > 0 {
		t.Errorf("a valid approval block was refused: %v", problems)
	}
	if ok.ApprovalMode != "return" || ok.HighRisk.Action != "require_approval" {
		t.Error("the validator changed the caller's block")
	}
}
