package aianalyzer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

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

// maxAnalyzedBodyBytes caps how much of the request body is sent to the model,
// bounding latency and token cost for large payloads.
const maxAnalyzedBodyBytes = 8 * 1024

// HTTPDecision is the result of analyzing a single request made through a native
// HTTP resource (httpproxy/kubernetes/claude-code).
type HTTPDecision struct {
	Outcome     Outcome
	RiskLevel   RiskLevel
	Title       string
	Explanation string
	RuleName    string
}

// AnalyzeHTTPRequest evaluates a single request flowing through a native HTTP
// resource and maps the connection's configured risk tier to a per-request
// outcome.
//
// Outcome mapping (per-request HTTP semantics):
//   - allow_execution        -> OutcomeAllow (forward, no alert)
//   - block_execution        -> OutcomeBlock (reject this request only)
//   - require_access_request  -> OutcomeWarn  (record/alert + forward)
//
// require_access_request degrades to warn because interactive per-request
// approval cannot work for proxied HTTP traffic: a request is synchronous and
// short-lived while human review is asynchronous, and for WebSocket sessions
// only the upgrade request is ever observed. Approval for HTTP resources is
// enforced at credential-issuance time (the JIT/access-request flow), not per
// request.
//
// Returns (nil, nil) when the connection has no analyzer rule configured — the
// feature is opt-in per connection.
func AnalyzeHTTPRequest(ctx context.Context, orgID uuid.UUID, connectionName, method, target string, body []byte) (*HTTPDecision, error) {
	rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, connectionName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed obtaining ai session analyzer rule for connection %q: %w", connectionName, err)
	}

	content := buildHTTPContent(method, target, body)
	res, err := AnalyzeSession(ctx, orgID, content, rule.CustomPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed analyzing http request: %w", err)
	}

	tier := rule.RiskEvaluation.Tier(riskLevelKey(res.RiskLevel))
	return &HTTPDecision{
		Outcome:     outcomeForAction(tier.Action),
		RiskLevel:   res.RiskLevel,
		Title:       res.Title,
		Explanation: res.Explanation,
		RuleName:    rule.Name,
	}, nil
}

func outcomeForAction(action models.RiskEvaluationAction) Outcome {
	switch action {
	case models.BlockExecution:
		return OutcomeBlock
	case models.RequireAccessRequest:
		return OutcomeWarn
	default:
		return OutcomeAllow
	}
}

func riskLevelKey(level RiskLevel) models.RiskLevelKey {
	switch level {
	case RiskLevelHigh:
		return models.RiskLevelKeyHigh
	case RiskLevelMedium:
		return models.RiskLevelKeyMedium
	default:
		return models.RiskLevelKeyLow
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
