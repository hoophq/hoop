package sidecar

import (
	"context"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/analyzer"
	"github.com/hoophq/hoopinspect/policy"
)

func aiRule(name string) policy.Rule {
	return policy.Rule{
		Name: name,
		Type: policy.MatchAIAnalysis,
		Trigger: &policy.AITrigger{
			Operations: []hoopinspect.Operation{hoopinspect.OpDelete},
		},
		HighRisk: "block",
	}
}

func pgLane(rules ...policy.Rule) *Config {
	return &Config{
		Policy: PolicyConfig{Enforce: ptr(true)},
		Listeners: []ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Policy: &PolicyConfig{Rules: rules},
		}},
	}
}

// An ai_analysis rule with no analyzer section would load and never fire.
// This package refuses that shape everywhere else and must refuse it here.
func TestAIRuleWithoutAnalyzerSectionIsRefused(t *testing.T) {
	err := pgLane(aiRule("risky")).Validate()
	if err == nil {
		t.Fatal("an ai_analysis rule with no analyzer section was accepted")
	}
	if !strings.Contains(err.Error(), "analyzer") {
		t.Errorf("the error does not name the missing section: %v", err)
	}
}

// An empty trigger classifies nothing, so the rule is a guardrail that does
// not run. Accepting it silently is the pii-entity failure again.
func TestAIRuleWithoutTriggerIsRefused(t *testing.T) {
	r := aiRule("risky")
	r.Trigger = nil
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("an ai_analysis rule with no trigger was accepted")
	}
	if !strings.Contains(err.Error(), "trigger") {
		t.Errorf("the error does not name the trigger: %v", err)
	}
}

// A rule naming no action for any level would allow every verdict, which
// looks like enforcement and is not.
func TestAIRuleWithoutAnyActionIsRefused(t *testing.T) {
	r := aiRule("risky")
	r.HighRisk = ""
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	if err := cfg.Validate(); err == nil {
		t.Fatal("an ai_analysis rule with no actions was accepted")
	}
}

// require_review is in the enum so the schema is stable when review lands,
// and refused here so nobody ships a config that appears to hold statements
// for approval and quietly does not.
func TestRequireReviewIsRefusedAtConfig(t *testing.T) {
	r := aiRule("risky")
	r.HighRisk = "require_review"
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("require_review was accepted")
	}
	if !strings.Contains(err.Error(), "require_review") {
		t.Errorf("the error does not name the action: %v", err)
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	r := aiRule("risky")
	r.HighRisk = "explode"
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	if err := cfg.Validate(); err == nil {
		t.Fatal("an unknown action was accepted")
	}
}

// Rules must never evaluate an ai_analysis rule: it needs a provider and a
// deadline. One reaching NewRules means a caller skipped the split, and
// accepting it would leave a rule that parses and matches nothing.
func TestPolicyRulesRefusesAIAnalysisType(t *testing.T) {
	_, err := policy.NewRules([]policy.Rule{aiRule("risky")})
	if err == nil {
		t.Fatal("policy.NewRules accepted an ai_analysis rule")
	}
}

// The split must keep ai rules out of the local set while preserving the
// order of what remains: first match still wins among locals.
func TestSplitAnalyzerRulesPreservesLocalOrder(t *testing.T) {
	local, ai := splitAnalyzerRules([]policy.Rule{
		{Name: "a", Type: policy.MatchOperation},
		aiRule("ai-1"),
		{Name: "b", Type: policy.MatchTable},
		aiRule("ai-2"),
	})

	if len(local) != 2 || local[0].Name != "a" || local[1].Name != "b" {
		t.Errorf("local rules = %v, want [a b] in order", names(local))
	}
	if len(ai) != 2 || ai[0].Name != "ai-1" || ai[1].Name != "ai-2" {
		t.Errorf("ai rules = %v, want [ai-1 ai-2]", names(ai))
	}
}

// An http block on a postgres lane would load and do nothing.
func TestHTTPBlockOnNonHTTPLaneIsRefused(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			HTTP: &HTTPCodecConfig{CaptureBody: true},
		}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("an http block on a postgres lane was accepted")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// A lane exposing Authorization to policy has put a bearer token into every
// decision log, audit record and prompt. One line of YAML must not be able to
// do that.
func TestForbiddenHeadersAreRefused(t *testing.T) {
	for _, name := range []string{"Authorization", "authorization", "Cookie", "Proxy-Authorization"} {
		cfg := &Config{
			Listeners: []ListenerConfig{{
				Name: "api", Protocol: "http", Listen: ":1", Upstream: "h:1",
				HTTP: &HTTPCodecConfig{CaptureBody: true, Headers: []string{name}},
			}},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("header %q was accepted into the policy allowlist", name)
		}
	}
}

func TestOrdinaryHeaderIsAccepted(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{
			Name: "api", Protocol: "http", Listen: ":1", Upstream: "h:1",
			HTTP: &HTTPCodecConfig{CaptureBody: true, Headers: []string{"Content-Type"}},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("an ordinary header was refused: %v", err)
	}
}

// A credential in an endpoint URL would be published by GET /config, which
// reports the analyzer endpoint.
func TestEndpointCarryingCredentialsIsRefused(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:pass@llm.internal/v1/messages",
		"https://llm.internal/v1/messages?api_key=secret",
	} {
		cfg := &AnalyzerConfig{Provider: "stub", Model: "m", Endpoint: endpoint}
		if problems := cfg.validate(false); len(problems) == 0 {
			t.Errorf("endpoint %q was accepted", endpoint)
		}
	}
}

// endpointHost must never render anything but a host, since that string goes
// into the admin view.
func TestEndpointHostRendersHostOnly(t *testing.T) {
	cfg := &AnalyzerConfig{Endpoint: "https://llm.internal:8443/v1/messages"}
	if got := cfg.endpointHost(); got != "llm.internal:8443" {
		t.Errorf("endpointHost() = %q, want the host only", got)
	}
}

// send=redacted with no detector would transmit raw text under a name that
// promises otherwise.
func TestRedactedSendWithoutDetectorIsRefused(t *testing.T) {
	cfg := &AnalyzerConfig{Provider: "stub", Model: "m", Send: SendRedacted}
	problems := cfg.validate(false)
	if len(problems) == 0 {
		t.Fatal("send=redacted with no pii section was accepted")
	}
	if !strings.Contains(strings.Join(problems, " "), "pii") {
		t.Errorf("the error does not name the missing section: %v", problems)
	}
}

func TestRedactedSendWithDetectorIsAccepted(t *testing.T) {
	cfg := &AnalyzerConfig{Provider: "stub", Model: "m", Send: SendRedacted}
	for _, p := range cfg.validate(true) {
		if strings.Contains(p, "pii") {
			t.Errorf("send=redacted was refused despite a detector: %v", p)
		}
	}
}

// A provider the binary does not link must be named as such, with the list of
// what IS linked: the usual cause is a config asking for vertex from a build
// that omitted it, and the bare name sends an operator to the wrong file.
func TestUnlinkedProviderNamesWhatIsLinked(t *testing.T) {
	cfg := &AnalyzerConfig{Provider: "definitely-not-linked", Model: "m"}
	problems := cfg.validate(false)
	if len(problems) == 0 {
		t.Fatal("an unlinked provider was accepted")
	}
	if !strings.Contains(strings.Join(problems, " "), "not linked") {
		t.Errorf("the error does not say the provider is unlinked: %v", problems)
	}
}

// fail_open defaults to TRUE here, unlike every other evaluator, because a
// classifier that denies during a provider outage takes the database down.
func TestFailOpenDefaultsTrue(t *testing.T) {
	if !(&AnalyzerConfig{}).failOpen() {
		t.Error("fail_open defaulted to false")
	}
	if (&AnalyzerConfig{FailOpen: ptr(false)}).failOpen() {
		t.Error("an explicit fail_open=false was ignored")
	}
}

// A lane with capture off must keep the registry default, so every lane that
// did not ask for anything follows the original code path.
func TestCodecFactoryIsNilWithoutHTTPConfig(t *testing.T) {
	if f := httpCodecFactory(hoopinspect.HTTP, nil); f != nil {
		t.Error("a lane with no http block got a custom codec factory")
	}
	if f := httpCodecFactory(hoopinspect.Postgres, &HTTPCodecConfig{CaptureBody: true}); f != nil {
		t.Error("a postgres lane got an http codec factory")
	}
}

// The factory must produce a FRESH codec per call: two connections sharing one
// stateful codec corrupt each other's reassembly buffer.
func TestCodecFactoryReturnsDistinctCodecs(t *testing.T) {
	f := httpCodecFactory(hoopinspect.HTTP, &HTTPCodecConfig{CaptureBody: true})
	if f == nil {
		t.Fatal("no factory for an http lane with capture on")
	}
	a, b := f(), f()
	if a == b {
		t.Error("the factory returned the same codec twice")
	}
	if a.Protocol() != hoopinspect.HTTP {
		t.Errorf("factory produced a %q codec", a.Protocol())
	}
}

// A rule's own prompt beats the analyzer default, which beats the built-in.
// Deployment-wide context lives in analyzer.prompt; what one rule protects
// lives on the rule.
func TestPromptPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rulePrompt string
		cfgPrompt  string
		want       string
	}{
		{"rule wins", "rule text", "cfg text", "rule text"},
		{"config default applies", "", "cfg text", "cfg text"},
		{"built-in when neither", "", "", "Risk levels:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := aiRule("risky")
			r.Prompt = tc.rulePrompt
			cfg := &AnalyzerConfig{Provider: "stub", Model: "m", Prompt: tc.cfgPrompt}

			evs, err := buildAnalyzerEvaluators([]policy.Rule{r}, cfg, stubAnalyzerProvider{}, nil)
			if err != nil {
				t.Fatalf("buildAnalyzerEvaluators: %v", err)
			}
			ev := evs[0].(*analyzer.Evaluator)
			if got := ev.SystemPrompt(); !strings.Contains(got, tc.want) {
				t.Errorf("prompt = %q, want it to contain %q", got, tc.want)
			}
			// Whatever the source, the contract survives.
			if !strings.Contains(ev.SystemPrompt(), "Never quote a literal value") {
				t.Error("the output contract was dropped")
			}
		})
	}
}

type stubAnalyzerProvider struct{}

func (stubAnalyzerProvider) Name() string { return "stub" }

func (stubAnalyzerProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskLow}, nil
}
