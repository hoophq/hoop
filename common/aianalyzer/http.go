package aianalyzer

import "strings"

// Outcome is the per-request action decided by the HTTP session analyzer.
type Outcome string

const (
	// OutcomeAllow forwards the request unchanged.
	OutcomeAllow Outcome = "allow"
	// OutcomeWarn records/alerts on the request but still forwards it.
	OutcomeWarn Outcome = "warn"
	// OutcomeBlock rejects this single request without ending the session.
	OutcomeBlock Outcome = "block"
)

// Action is the configured response for a risk tier. It mirrors the gateway's
// models.RiskEvaluationAction by value so this package stays free of the data
// layer.
type Action string

const (
	ActionAllowExecution       Action = "allow_execution"
	ActionBlockExecution       Action = "block_execution"
	ActionRequireAccessRequest Action = "require_access_request"
)

// maxAnalyzedBodyBytes caps how much of the request body is sent to the model,
// bounding latency and token cost for large payloads.
const maxAnalyzedBodyBytes = 8 * 1024

// RuleConfig is the per-connection analyzer rule resolved by the gateway and
// shipped to the agent. It maps each risk tier to a configured action.
type RuleConfig struct {
	Name             string
	CustomPrompt     string
	LowRiskAction    Action
	MediumRiskAction Action
	HighRiskAction   Action
}

// Decision is the result of analyzing a single HTTP request.
type Decision struct {
	Outcome     Outcome
	RiskLevel   RiskLevel
	Title       string
	Explanation string
	RuleName    string
}

// actionForRisk returns the configured action for the classified risk level,
// defaulting to allow when a tier has no action set.
func (r RuleConfig) actionForRisk(level RiskLevel) Action {
	var action Action
	switch level {
	case RiskLevelHigh:
		action = r.HighRiskAction
	case RiskLevelMedium:
		action = r.MediumRiskAction
	default:
		action = r.LowRiskAction
	}
	if action == "" {
		return ActionAllowExecution
	}
	return action
}

// outcomeForAction maps a configured tier action to a per-request HTTP outcome.
//
// require_access_request degrades to warn because interactive per-request
// approval cannot work for proxied HTTP traffic: a request is synchronous and
// short-lived while human review is asynchronous, and for WebSocket sessions
// only the upgrade request is ever observed. Approval for HTTP resources is
// enforced at credential-issuance time (the JIT/access-request flow), not per
// request.
func outcomeForAction(action Action) Outcome {
	switch action {
	case ActionBlockExecution:
		return OutcomeBlock
	case ActionRequireAccessRequest:
		return OutcomeWarn
	default:
		return OutcomeAllow
	}
}

// buildHTTPContent renders the request line and (capped) body into the text the
// model classifies. Only the method, target, and body are included — request
// headers are intentionally omitted to avoid leaking the proxy auth token or
// other sensitive headers into the model context.
func buildHTTPContent(method, target string, body []byte) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteString(" ")
	b.WriteString(target)
	if len(body) > 0 {
		b.WriteString("\n\n")
		if len(body) > maxAnalyzedBodyBytes {
			b.Write(body[:maxAnalyzedBodyBytes])
			b.WriteString("\n...[truncated]")
		} else {
			b.Write(body)
		}
	}
	return b.String()
}
