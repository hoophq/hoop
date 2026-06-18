package aianalyzer

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func TestOutcomeForAction(t *testing.T) {
	cases := []struct {
		action models.RiskEvaluationAction
		want   Outcome
	}{
		{models.AllowExecution, OutcomeAllow},
		{models.BlockExecution, OutcomeBlock},
		// Per-request HTTP cannot do interactive approval, so it degrades to warn.
		{models.RequireAccessRequest, OutcomeWarn},
		{models.RiskEvaluationAction(""), OutcomeAllow},
		{models.RiskEvaluationAction("unknown"), OutcomeAllow},
	}
	for _, tc := range cases {
		if got := outcomeForAction(tc.action); got != tc.want {
			t.Errorf("outcomeForAction(%q) = %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestRiskLevelKey(t *testing.T) {
	cases := []struct {
		level RiskLevel
		want  models.RiskLevelKey
	}{
		{RiskLevelHigh, models.RiskLevelKeyHigh},
		{RiskLevelMedium, models.RiskLevelKeyMedium},
		{RiskLevelLow, models.RiskLevelKeyLow},
		{RiskLevel("bogus"), models.RiskLevelKeyLow},
	}
	for _, tc := range cases {
		if got := riskLevelKey(tc.level); got != tc.want {
			t.Errorf("riskLevelKey(%q) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestBuildHTTPContent(t *testing.T) {
	t.Run("no body", func(t *testing.T) {
		got := buildHTTPContent("GET", "/api/pods?ns=prod", nil)
		if got != "GET /api/pods?ns=prod" {
			t.Errorf("unexpected content: %q", got)
		}
	})

	t.Run("with body", func(t *testing.T) {
		got := buildHTTPContent("POST", "/v1/messages", []byte(`{"q":"drop table users"}`))
		want := "POST /v1/messages\n\n{\"q\":\"drop table users\"}"
		if got != want {
			t.Errorf("unexpected content: %q", got)
		}
	})

	t.Run("body is truncated", func(t *testing.T) {
		body := []byte(strings.Repeat("a", maxAnalyzedBodyBytes+100))
		got := buildHTTPContent("PUT", "/x", body)
		if !strings.HasSuffix(got, "\n...[truncated]") {
			t.Errorf("expected truncation marker, got suffix %q", got[len(got)-32:])
		}
		// request line + "\n\n" + capped body + marker
		if len(got) > len("PUT /x")+2+maxAnalyzedBodyBytes+len("\n...[truncated]") {
			t.Errorf("content longer than cap: %d bytes", len(got))
		}
	})
}
