package models

import "testing"

func TestRiskEvaluationActionIsValid(t *testing.T) {
	cases := []struct {
		action RiskEvaluationAction
		want   bool
	}{
		{AllowExecution, true},
		{BlockExecution, true},
		{RequireAccessRequest, true},
		{RiskEvaluationAction(""), false},
		{RiskEvaluationAction("bogus"), false},
	}
	for _, tc := range cases {
		if got := tc.action.IsValid(); got != tc.want {
			t.Errorf("IsValid(%q) = %v, want %v", tc.action, got, tc.want)
		}
	}
}

func TestRiskEvaluationTier(t *testing.T) {
	ruleName := "escalate"
	structured := &AISessionAnalyzerRiskTier{Action: RequireAccessRequest, AccessRequestRuleName: &ruleName}

	cases := []struct {
		name       string
		eval       *AISessionAnalyzerRiskEvaluation
		level      RiskLevelKey
		wantAction RiskEvaluationAction
		wantRule   *string
	}{
		{
			name:       "nil evaluation defaults to allow",
			eval:       nil,
			level:      RiskLevelKeyHigh,
			wantAction: AllowExecution,
		},
		{
			name:       "empty tier defaults to allow",
			eval:       &AISessionAnalyzerRiskEvaluation{},
			level:      RiskLevelKeyLow,
			wantAction: AllowExecution,
		},
		{
			name:       "structured tier takes precedence over legacy",
			eval:       &AISessionAnalyzerRiskEvaluation{HighRisk: structured, HighRiskAction: AllowExecution},
			level:      RiskLevelKeyHigh,
			wantAction: RequireAccessRequest,
			wantRule:   &ruleName,
		},
		{
			name:       "falls back to legacy action when no structured tier",
			eval:       &AISessionAnalyzerRiskEvaluation{MediumRiskAction: BlockExecution},
			level:      RiskLevelKeyMedium,
			wantAction: BlockExecution,
		},
		{
			name:       "resolves per level",
			eval:       &AISessionAnalyzerRiskEvaluation{LowRiskAction: AllowExecution, HighRiskAction: BlockExecution},
			level:      RiskLevelKeyHigh,
			wantAction: BlockExecution,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.eval.Tier(tc.level)
			if got.Action != tc.wantAction {
				t.Errorf("Tier(%q).Action = %q, want %q", tc.level, got.Action, tc.wantAction)
			}
			switch {
			case tc.wantRule == nil && got.AccessRequestRuleName != nil:
				t.Errorf("Tier(%q).AccessRequestRuleName = %q, want nil", tc.level, *got.AccessRequestRuleName)
			case tc.wantRule != nil && (got.AccessRequestRuleName == nil || *got.AccessRequestRuleName != *tc.wantRule):
				t.Errorf("Tier(%q).AccessRequestRuleName = %v, want %q", tc.level, got.AccessRequestRuleName, *tc.wantRule)
			}
		})
	}
}
