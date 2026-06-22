// Package aianalyzer is the gateway-side façade over the shared AI session risk
// classifier that lives in github.com/hoophq/hoop/common/aianalyzer.
//
// The engine (system prompt, tool schema, provider clients, classification)
// lives in common so it ships in both the OSS and enterprise builds and is
// shared with the agent's HTTP proxy (which injects it into libhoop) without
// duplication. This package keeps the gateway's exec-path entrypoint
// (AnalyzeSession), loading the org's AI provider from the data layer
// (gateway/models) and delegating the actual classification to the engine.
// Risk-tier → action mapping stays in the data layer (models).
package aianalyzer

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	laia "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/gateway/models"
)

// RiskLevel re-exports the engine's risk level so existing gateway callers keep
// their type without importing libhoop directly.
type RiskLevel = laia.RiskLevel

const (
	RiskLevelLow    = laia.RiskLevelLow
	RiskLevelMedium = laia.RiskLevelMedium
	RiskLevelHigh   = laia.RiskLevelHigh
)

// SessionAnalysisResult re-exports the engine's classification result.
type SessionAnalysisResult = laia.Result

// SessionAnalyzerSystemPrompt is the default classifier prompt. It is surfaced
// in the admin UI as the editable default and is owned by the engine.
const SessionAnalyzerSystemPrompt = laia.SystemPrompt

// RiskLevelKey maps the engine's risk level to the data-layer key used by the
// risk-evaluation tier configuration.
func RiskLevelKey(level RiskLevel) models.RiskLevelKey {
	switch level {
	case RiskLevelHigh:
		return models.RiskLevelKeyHigh
	case RiskLevelMedium:
		return models.RiskLevelKeyMedium
	default:
		return models.RiskLevelKeyLow
	}
}

// AnalyzeSession loads the org's configured AI provider and classifies the
// given command/query/script, returning a risk level with a short title and
// explanation.
//
// customPrompt, when non-nil and non-empty, is prepended to the default system
// prompt while preserving the tool-calling contract.
func AnalyzeSession(ctx context.Context, orgID uuid.UUID, content string, customPrompt *string) (*SessionAnalysisResult, error) {
	provider, err := models.GetAIProvider(orgID, models.AISessionAnalyzerFeature)
	if err != nil || provider == nil {
		return nil, fmt.Errorf("session analyzer: failed to load ai provider: %w", err)
	}

	client, err := laia.NewLLMClient(laia.ProviderConfig{
		Provider: provider.Provider,
		APIURL:   provider.ApiUrl,
		APIKey:   provider.ApiKey,
		Model:    provider.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("session analyzer: failed to create ai client: %w", err)
	}

	var prompt string
	if customPrompt != nil {
		prompt = *customPrompt
	}
	return laia.Analyze(ctx, client, content, prompt)
}
