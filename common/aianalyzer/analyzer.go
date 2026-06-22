package aianalyzer

import "context"

// Analyzer classifies a single HTTP request and returns a per-request decision.
// The agent holds one (or nil) per connection and consults it inline — through
// the libhoop adapter — before forwarding each request upstream.
type Analyzer interface {
	AnalyzeRequest(ctx context.Context, method, target string, body []byte) (*Decision, error)
}

type analyzerClient struct {
	llm  LLMClient
	rule RuleConfig
}

// NewClient builds an Analyzer from the resolved provider and rule
// configuration.
//
// The feature is opt-in per connection: callers only construct an analyzer when
// the connection has an analyzer rule and the org has an AI provider. An invalid
// provider configuration returns an error so the caller can fail open.
func NewClient(provider ProviderConfig, rule RuleConfig) (Analyzer, error) {
	llm, err := NewLLMClient(provider)
	if err != nil {
		return nil, err
	}
	return &analyzerClient{llm: llm, rule: rule}, nil
}

// AnalyzeRequest classifies the request and maps the configured tier action to
// a per-request outcome.
func (a *analyzerClient) AnalyzeRequest(ctx context.Context, method, target string, body []byte) (*Decision, error) {
	res, err := Analyze(ctx, a.llm, buildHTTPContent(method, target, body), a.rule.CustomPrompt)
	if err != nil {
		return nil, err
	}
	return &Decision{
		Outcome:     outcomeForAction(a.rule.actionForRisk(res.RiskLevel)),
		RiskLevel:   res.RiskLevel,
		Title:       res.Title,
		Explanation: res.Explanation,
		RuleName:    a.rule.Name,
	}, nil
}
