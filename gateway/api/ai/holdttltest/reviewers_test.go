package holdttltest

import (
	"net/http"
	"testing"

	apiai "github.com/hoophq/hoop/gateway/api/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With approvers, the analyzer routes ignore reviewers_groups: the managed
// rule takes the groups the human levels share, else the admin group.
func TestTheAnalyzerRoutesDeriveTheReviewersFromTheApprovers(t *testing.T) {
	startDB(t)
	rule := func(approvers string) string {
		return `{"name":"level-hold","connection_names":[],"agentic":false,` +
			`"risk_evaluation":{"low_risk_action":"allow_execution","medium_risk_action":"allow_execution","high_risk_action":"allow_execution"},` +
			`"reviewers_groups":["requested"],` +
			`"sidecar_spec":{"high":"require_approval","medium":"require_approval","approval_rule":"level-hold",` +
			`"approvers":` + approvers + `}}`
	}

	rec, out := callRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
		rule(`{"high":{"type":"human","groups":["dba"]},"medium":{"type":"human","groups":["dba"]}}`))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, []any{"dba"}, out["reviewers_groups"], "create")

	rec, out = callRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, "level-hold",
		rule(`{"high":{"type":"human","groups":["dba"]},"medium":{"type":"human","groups":["sre"]}}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []any{"admin"}, out["reviewers_groups"], "update")
}
