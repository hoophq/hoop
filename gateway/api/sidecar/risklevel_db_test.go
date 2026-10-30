package apisidecar

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// levelSpec holds every level under the rule's own approval rule: high for
// dba, medium for the administrators, and low for the approval agent.
func levelSpec(ruleName string) string {
	return `{"high": "require_approval", "medium": "require_approval", "low": "require_approval",
		"approval_rule": "` + ruleName + `",
		"approvers": {
			"high": {"type": "human", "groups": ["dba"]},
			"medium": {"type": "human"},
			"low": {"type": "ai_agent"}}}`
}

// A hold stored without approvers is served unchanged to every build, files
// under the rule's own managed rule, and is released by its groups.
func TestAStoredBareHoldUnderTheManagedRuleKeepsWorking(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	const spec = `{"high":"require_review","medium":"require_approval","approval_rule":"stored-hold"}`
	sc := seedLevelSidecar(t, "stored", "stored-hold", spec)
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, "stored-hold", json.RawMessage(spec), &[]string{"dba"}))
	groups, err := services.AnalyzerApprovalReviewers(models.DB, org, "stored-hold")
	require.NoError(t, err)
	require.Equal(t, []string{"dba"}, groups)

	for _, caps := range []string{"", daemon.CapabilityLevelApprovers} {
		w := handshake(t, sc, caps)
		require.Equal(t, http.StatusOK, w.Code, "caps %q: %s", caps, w.Body.String())
		var served daemon.Config
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
		la := served.Listeners[0].Analyzer
		require.NotNil(t, la, caps)
		assert.Equal(t, "require_review", la.HighRisk, caps)
		assert.Equal(t, "require_review", la.MediumRisk, caps)
		assert.Equal(t, "stored-hold", la.ApprovalRule, caps)
		assert.NotContains(t, w.Body.String(), `"approvers"`, caps)

		statement := "DELETE FROM payouts WHERE build = '" + caps + "'"
		rec := postLevelBody(sc, statement, map[string]any{"approval_rule": "stored-hold"})
		require.Equal(t, http.StatusCreated, rec.Code, "caps %q: %s", caps, rec.Body.String())
		filed := decodeReviewResponse(t, rec)
		require.NotNil(t, filed.Review.AccessRequestRuleName)
		assert.Equal(t, "stored-hold", *filed.Review.AccessRequestRuleName)
		assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review), "the managed rule's groups")
		assert.Nil(t, filed.Review.RiskLevel)

		rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"sre"})
		assert.Equal(t, http.StatusNotFound, rec.Code, "caps %q: outside the groups: %s", caps, rec.Body.String())
		rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"dba"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		claim := decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID))
		assert.True(t, claim.Forward, caps)
	}
}

// seedLevelSidecar stores a sidecar whose appdb listener runs the analyzer
// rule ruleName, bound in the control plane, with its managed approval rule.
func seedLevelSidecar(t *testing.T, name, ruleName, spec string) *models.Sidecar {
	t.Helper()
	org := uuid.MustParse(statusTestOrgID)
	sc := &models.Sidecar{
		OrgID:     statusTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
		}}},
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	rule := &models.AISessionAnalyzerRules{OrgID: org, Name: ruleName, ConnectionNames: []string{},
		SidecarSpec: json.RawMessage(spec),
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRisk:    &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
			MediumRisk: &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
			HighRisk:   &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
		}}
	require.NoError(t, models.CreateAISessionAnalyzerRuleTx(models.DB, rule))
	require.NoError(t, models.SetAnalyzerRuleListenersTx(models.DB, org, ruleName,
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}))
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, ruleName, rule.SidecarSpec, nil))
	return sc
}

// setLevelSpec replaces the spec of an analyzer rule, as a save does.
func setLevelSpec(t *testing.T, ruleName, spec string) {
	t.Helper()
	org := uuid.MustParse(statusTestOrgID)
	require.NoError(t, models.DB.Exec(`UPDATE private.ai_session_analyzer_rules SET sidecar_spec = ?::jsonb
		WHERE org_id = ? AND name = ?`, spec, org, ruleName).Error)
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, ruleName, json.RawMessage(spec), nil))
}

// postLevelBody runs POST /sidecars/reviews with body, and the statement as
// its payload.
func postLevelBody(sc *models.Sidecar, statement string, body map[string]any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	body["payload"] = base64.StdEncoding.EncodeToString([]byte(statement))
	if _, ok := body["listener_name"]; !ok {
		body["listener_name"] = "appdb"
	}
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", strings.NewReader(string(raw)))
	c.Request.Header.Set("Content-Type", "application/json")
	PostReview(c)
	return rec
}

func fileLevel(sc *models.Sidecar, statement, level string) *httptest.ResponseRecorder {
	return postLevelBody(sc, statement, map[string]any{"risk_level": level})
}

func reviewGroupNames(rev *openapi.Review) []string {
	var out []string
	for _, g := range rev.ReviewGroupsData {
		out = append(out, g.Group)
	}
	return out
}

func TestALevelReviewIsAuthorizedByTheLevelGroups(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "levels", "levels-rule", levelSpec("levels-rule"))
	const statement = "DELETE FROM payments WHERE id = 1"

	// high: dba approves, one approval, no access request rule.
	rec := fileLevel(sc, statement, "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	assert.False(t, filed.Forward)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	require.NotNil(t, filed.Review.MinApprovals)
	assert.Equal(t, 1, *filed.Review.MinApprovals)
	assert.Nil(t, filed.Review.AccessRequestRuleName)
	require.NotNil(t, filed.Review.RiskLevel)
	assert.Equal(t, "high", *filed.Review.RiskLevel)

	// A retry of the same bytes answers from the same review.
	retry := decodeReviewResponse(t, fileLevel(sc, statement, "high"))
	assert.Equal(t, filed.Review.ID, retry.Review.ID)
	// Another level is another review.
	other := decodeReviewResponse(t, fileLevel(sc, statement, "medium"))
	assert.NotEqual(t, filed.Review.ID, other.Review.ID)
	// A rule filing of the same bytes never matches it.
	_, err := models.GetLiveSidecarReview(models.DB, statusTestOrgID, sc.ID, "appdb",
		models.SidecarReviewKey{RuleName: "levels-rule"}, models.HashStatement([]byte(statement)), time.Now().UTC())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// The status read names the level.
	statusRec := getReview(sc, filed.Review.ID)
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
	var status openapi.SidecarReviewStatus
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	assert.Equal(t, "high", status.RiskLevel)
	assert.Empty(t, status.ApprovalRule)

	// Someone outside the level groups cannot see it, so cannot approve it.
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"sre"})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	held, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, filed.Review.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReviewStatusPending, held.Status)
	// A member approves, and the claim releases the statement once.
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	claim := decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID))
	assert.True(t, claim.Forward)
	again := decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID))
	assert.False(t, again.Forward, "an approval releases one statement")

	// medium names no groups: the administrators approve.
	assert.Equal(t, []string{types.GroupAdmin}, reviewGroupNames(other.Review))
	rec = putReview(t, other.Review.ID, `{"status":"approved"}`, []string{types.GroupAdmin})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The limits come from the block's approval_rule, here the managed rule.
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, uuid.MustParse(statusTestOrgID),
		"levels-rule", &pending, nil))
	timed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 3", "high"))
	require.NotNil(t, timed.Review.ExpiresAt, "the level review takes the approval rule's pending limit")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), *timed.Review.ExpiresAt, time.Minute)
}

// A level filing needs a level that holds with a human approvers entry. The
// body names only the level, so it cannot pick its approvers.
func TestALevelFilingIsRefusedUnlessAHumanHoldsTheLevel(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "mismatch", "mismatch-rule", `{"high": "require_approval", "medium": "require_approval",
		"low": "block", "approval_rule": "mismatch-rule",
		"approvers": {"high": {"type": "human"}, "low": {"type": "human", "groups": ["dba"]}}}`)
	agent := seedLevelSidecar(t, "agent-level", "agent-level-rule", levelSpec("agent-level-rule"))

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"an ai_agent level":                    fileLevel(agent, "DELETE FROM t", "low"),
		"a held level with no approvers entry": fileLevel(sc, "DELETE FROM t", "medium"),
		"a level that does not hold":           fileLevel(sc, "DELETE FROM t", "low"),
		"a listener the sidecar does not have": postLevelBody(sc, "DELETE FROM t", map[string]any{"listener_name": "other", "risk_level": "high"}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "is not configured to hold the", name)
	}

	for name, body := range map[string]map[string]any{
		"neither rule nor level": {},
		"both rule and level":    {"approval_rule": "mismatch-rule", "risk_level": "high"},
		"an unknown level":       {"risk_level": "critical"},
	} {
		rec := postLevelBody(sc, "DELETE FROM t", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
	}

	// The held human level files.
	assert.Equal(t, http.StatusCreated, fileLevel(sc, "DELETE FROM t", "high").Code)
}

// A build that does not read approvers gets every held level under the
// block's approval_rule, and files there. A block with approvers and no
// approval_rule is refused to that build, naming the capability.
func TestAnOldBuildIsServedTheHeldLevelsUnderTheApprovalRule(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	spec := levelSpec("old-rule")
	sc := seedLevelSidecar(t, "old-build", "old-rule", spec)
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, "old-rule", json.RawMessage(spec), &[]string{"dba"}))

	w := handshake(t, sc, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"approvers"`)
	var served daemon.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	la := served.Listeners[0].Analyzer
	require.NotNil(t, la)
	assert.Nil(t, la.Approvers)
	assert.Equal(t, "old-rule", la.ApprovalRule)
	for _, action := range []string{la.HighRisk, la.MediumRisk, la.LowRisk} {
		assert.Equal(t, analyzer.ActionRequireReview, analyzer.Action(action).Canonical(),
			"an ai_agent level becomes a human hold: more oversight, never less")
	}

	// The old build files under the rule, and its groups release it.
	rec := postLevelBody(sc, "DELETE FROM payouts WHERE id = 1", map[string]any{"approval_rule": "old-rule"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	require.NotNil(t, filed.Review.AccessRequestRuleName)
	assert.Equal(t, "old-rule", *filed.Review.AccessRequestRuleName)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID)).Forward)

	// A build that reads approvers gets them, and the stored row is unchanged.
	w = handshake(t, sc, daemon.CapabilityLevelApprovers)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"approvers"`)
	// The level filing is authorized against the stored rules, whatever the build.
	assert.Equal(t, http.StatusCreated, fileLevel(sc, "DELETE FROM payouts WHERE id = 2", "high").Code)

	// No approval_rule: nothing to fall back on, so the old build is refused.
	setLevelSpec(t, "old-rule", `{"high": "require_approval", "approvers": {"high": {"type": "human", "groups": ["dba"]}}}`)
	w = handshake(t, sc, "")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), daemon.CapabilityLevelApprovers)
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastOutcome)
	assert.Equal(t, models.SidecarOutcomeNotServed, *row.LastOutcome)
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, daemon.CapabilityLevelApprovers)

	// The build with the capability is still served.
	w = handshake(t, sc, daemon.CapabilityLevelApprovers)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
