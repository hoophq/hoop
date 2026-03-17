package apiai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/aiclients"
	"github.com/hoophq/hoop/gateway/models"
)

// RiskLevel represents the assessed risk of a session command or query.
type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

// SessionAnalysisResult holds the outcome of an AI analysis.
type SessionAnalysisResult struct {
	RiskLevel   RiskLevel
	Title       string
	Explanation string
}

const sessionAnalyzerSystemPrompt = `You are a security-focused execution risk classifier for commands, scripts, and database queries.

Goal: choose exactly ONE tool to call:
- LowRiskAISessionAnalyzer
- MediumRiskAISessionAnalyzer
- HighRiskAISessionAnalyzer

Classify the user's input by its likely impact if executed in a real DB/VM environment.

Risk rubric:
- High Risk: destructive/irreversible actions, privilege escalation, data exfiltration, credential access, disabling security, ransomware-like behavior, wiping disks, dropping tables, mass deletes/updates without constraints, remote code execution, persistence, network scanning/exploitation.
- Medium Risk: potentially expensive/unstable actions (locks, long scans), broad reads of sensitive data, schema changes with rollback risk, writes that are reversible but risky, commands that could disrupt service if misused.
- Low Risk: read-only, scoped, clearly non-destructive, routine diagnostics, safe formatting/linting, harmless queries with tight filters/limits.

Output rules (strict):
1) You MUST call exactly one tool. Do not produce normal text.
2) When uncertain, choose the higher risk.
3) Populate tool arguments:
   - title: <= 4 words, no punctuation if possible.
   - explanation: <= 30 words, concise and specific.
4) Do not mention policies. Do not mention tool names in the text fields.`

// riskToolSchema is shared by all three risk classifier tools.
var riskToolSchema = aiclients.ToolInputSchema{
	Properties: map[string]aiclients.ToolProperty{
		"title": {
			Type:        "string",
			Description: "Short label (max 4 words) summarizing the main concern or reassurance.",
		},
		"explanation": {
			Type:        "string",
			Description: "In <= 30 words, explain the risk level concretely.",
		},
	},
	Required: []string{"title", "explanation"},
}

var sessionAnalyzerTools = []aiclients.Tool{
	{
		Name:        "LowRiskAISessionAnalyzer",
		Description: "Select when the input is safe to execute: non-destructive, scoped, and low operational/security impact.",
		InputSchema: riskToolSchema,
	},
	{
		Name:        "MediumRiskAISessionAnalyzer",
		Description: "Select when the input could cause performance issues, service disruption, sensitive exposure, or risky-but-not-clearly-destructive changes.",
		InputSchema: riskToolSchema,
	},
	{
		Name:        "HighRiskAISessionAnalyzer",
		Description: "Select when the input is destructive, irreversible, escalates privileges, exfiltrates data, disables defenses, or resembles exploit/persistence behavior.",
		InputSchema: riskToolSchema,
	},
}

// AnalyzeSession sends the given command/query to the configured AI provider
// and returns a risk classification with a short title and explanation.
//
// The model is expected to call exactly one of the three risk tools; the tool
// name is mapped to RiskLevelLow / RiskLevelMedium / RiskLevelHigh.
func AnalyzeSession(ctx context.Context, orgID uuid.UUID, content string) (*SessionAnalysisResult, error) {
	provider, err := models.GetAIProvider(orgID, models.AISessionAnalyzerFeature)
	if err != nil || provider == nil {
		return nil, fmt.Errorf("session analyzer: failed to load ai provider: %w", err)
	}

	client, err := aiclients.NewClient(*provider)
	if err != nil {
		return nil, fmt.Errorf("session analyzer: failed to create ai client: %w", err)
	}

	resp, err := client.Chat(ctx, aiclients.ChatRequest{
		SystemPrompt: sessionAnalyzerSystemPrompt,
		Messages: []aiclients.Message{
			{Role: aiclients.RoleUser, Content: content},
		},
		Tools:      sessionAnalyzerTools,
		ToolChoice: aiclients.ToolChoiceAuto,
	})
	if err != nil {
		return nil, fmt.Errorf("session analyzer: chat request failed: %w", err)
	}
	if len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("session analyzer: model did not call a risk tool")
	}

	tc := resp.ToolCalls[0]
	var args struct {
		Title       string `json:"title"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return nil, fmt.Errorf("session analyzer: failed to parse tool arguments: %w", err)
	}

	var level RiskLevel
	switch tc.Name {
	case "LowRiskAISessionAnalyzer":
		level = RiskLevelLow
	case "MediumRiskAISessionAnalyzer":
		level = RiskLevelMedium
	case "HighRiskAISessionAnalyzer":
		level = RiskLevelHigh
	default:
		return nil, fmt.Errorf("session analyzer: unexpected tool call %q", tc.Name)
	}

	return &SessionAnalysisResult{
		RiskLevel:   level,
		Title:       args.Title,
		Explanation: args.Explanation,
	}, nil
}
