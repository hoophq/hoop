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
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// levelSpec holds high for dba, medium for the administrators, and low for
// the approval agent.
const levelSpec = `{
	"high": {"action": "require_approval", "approver": "human", "approval_groups": ["dba"]},
	"medium": {"action": "require_approval", "approver": "human"},
	"low": {"action": "require_approval", "approver": "ai_agent"}}`

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
	stored, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	return stored
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
	return postLevelBody(sc, statement, map[string]any{"risk_level": level, "approver": "human"})
}

func postAgentDecision(sc *models.Sidecar, statement, level, decision, reason string) *httptest.ResponseRecorder {
	return postLevelBody(sc, statement, map[string]any{"risk_level": level, "approver": "ai_agent",
		"decision": decision, "reason": reason})
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
	sc := seedLevelSidecar(t, "levels", "levels-rule", levelSpec)

	// high: dba approves, one approval, no access request rule.
	rec := fileLevel(sc, "DELETE FROM payments WHERE id = 1", "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	assert.False(t, filed.Forward)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	require.NotNil(t, filed.Review.MinApprovals)
	assert.Equal(t, 1, *filed.Review.MinApprovals)
	assert.Nil(t, filed.Review.AccessRequestRuleName)
	require.NotNil(t, filed.Review.RiskLevel)
	assert.Equal(t, "high", *filed.Review.RiskLevel)
	assert.Equal(t, models.ApproverTypeHuman, filed.Review.ApproverType)

	// A retry of the same bytes answers from the same review.
	retry := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 1", "high"))
	assert.Equal(t, filed.Review.ID, retry.Review.ID)
	// A rule filing of the same bytes never matches it.
	_, err := models.GetLiveSidecarReview(models.DB, statusTestOrgID, sc.ID, "appdb",
		models.SidecarReviewKey{RuleName: "levels-rule"}, models.HashStatement([]byte("DELETE FROM payments WHERE id = 1")),
		time.Now().UTC())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

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

	// medium names no groups: the administrators approve.
	rec = fileLevel(sc, "DELETE FROM payments WHERE id = 2", "medium")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	medium := decodeReviewResponse(t, rec)
	assert.Equal(t, []string{types.GroupAdmin}, reviewGroupNames(medium.Review))
	rec = putReview(t, medium.Review.ID, `{"status":"approved"}`, []string{types.GroupAdmin})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The limits come from the analyzer rule's managed approval rule.
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, uuid.MustParse(statusTestOrgID),
		"levels-rule", &pending, nil))
	timed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 3", "high"))
	require.NotNil(t, timed.Review.ExpiresAt, "the level review takes the managed rule's pending limit")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), *timed.Review.ExpiresAt, time.Minute)
}

func TestALevelThatDoesNotHoldForTheApproverIsRefused(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "mismatch", "mismatch-rule",
		`{"high":{"action":"require_approval","approver":"human"},"medium":"block",`+
			`"low":{"action":"require_approval","approver":"ai_agent"}}`)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a human filing on an agent level":    fileLevel(sc, "DELETE FROM t", "low"),
		"an agent decision on a human level":  postAgentDecision(sc, "DELETE FROM t", "high", "approved", "bounded"),
		"a filing on a level that holds none": fileLevel(sc, "DELETE FROM t", "medium"),
		"a listener the sidecar does not have": postLevelBody(sc, "DELETE FROM t",
			map[string]any{"listener_name": "other", "risk_level": "high", "approver": "human"}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "is not configured to hold the", name)
	}

	for name, body := range map[string]map[string]any{
		"neither rule nor level":    {},
		"both rule and level":       {"approval_rule": "r", "risk_level": "high", "approver": "human"},
		"an unknown level":          {"risk_level": "critical", "approver": "human"},
		"no approver":               {"risk_level": "high"},
		"a decision from a person":  {"risk_level": "high", "approver": "human", "decision": "approved", "reason": "r"},
		"an agent with no reason":   {"risk_level": "low", "approver": "ai_agent", "decision": "approved", "reason": " "},
		"an agent with no decision": {"risk_level": "low", "approver": "ai_agent", "reason": "r"},
		"an agent that reserves": {"risk_level": "low", "approver": "ai_agent", "decision": "approved",
			"reason": "r", "reserve": true},
		"a decision on a rule filing": {"approval_rule": "r", "decision": "approved"},
	} {
		rec := postLevelBody(sc, "DELETE FROM t", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
	}
}

func TestAnAgentDecisionIsFiledDecidedAndFinal(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "agent", "agent-rule", levelSpec)
	const statement = "UPDATE users SET plan = 'pro' WHERE id = 7"

	rec := postAgentDecision(sc, statement, "low", "approved", "The update names one row.")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	approved := decodeReviewResponse(t, rec)
	assert.False(t, approved.Forward, "the sidecar already decided; the plane releases nothing")
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), approved.Review.Status)
	assert.Equal(t, models.ApproverTypeAIAgent, approved.Review.ApproverType)
	require.NotNil(t, approved.Review.DecisionReason)
	assert.Equal(t, "The update names one row.", *approved.Review.DecisionReason)
	assert.Nil(t, approved.Review.RejectionReason)
	assert.Empty(t, approved.Review.ReviewGroupsData)

	stored, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, approved.Review.ID)
	require.NoError(t, err)
	assert.False(t, stored.StatementHash.Valid, "no filing can match an agent decision")
	sess, err := models.GetSessionByID(statusTestOrgID, stored.SessionID)
	require.NoError(t, err)
	assert.Equal(t, string(openapi.SessionStatusDone), sess.Status)
	require.NotNil(t, sess.Review)
	assert.Equal(t, models.ApproverTypeAIAgent, sess.Review.ApproverType)

	// Nothing claims it.
	claim := decodeReviewResponse(t, claimSidecarReview(sc, approved.Review.ID))
	assert.False(t, claim.Forward)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), claim.Review.Status)
	claimed, _, err := models.ClaimApprovedSidecarReview(models.DB, statusTestOrgID, approved.Review.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, claimed)

	// The status read names the agent and when it decided.
	statusRec := getReview(sc, approved.Review.ID)
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
	var status openapi.SidecarReviewStatus
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	assert.Equal(t, models.ApproverTypeAIAgent, status.ApproverType)
	assert.Equal(t, "low", status.RiskLevel)
	assert.NotNil(t, status.DecidedAt)

	// The same bytes again file a new decision, never the old one.
	rec = postAgentDecision(sc, statement, "low", "rejected", "The update has no WHERE clause.")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rejected := decodeReviewResponse(t, rec)
	assert.NotEqual(t, approved.Review.ID, rejected.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusRejected), rejected.Review.Status)
	require.NotNil(t, rejected.Review.RejectionReason)
	assert.Equal(t, "The update has no WHERE clause.", *rejected.Review.RejectionReason)

	// And a person's level files its own review for those bytes.
	human := decodeReviewResponse(t, fileLevel(sc, statement, "high"))
	assert.NotEqual(t, approved.Review.ID, human.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), human.Review.Status)

	// No person decides it again, an administrator included.
	for _, body := range []string{`{"status":"rejected"}`, `{"status":"revoked"}`, `{"status":"approved"}`} {
		rec = putReview(t, approved.Review.ID, body, []string{types.GroupAdmin})
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "approval agent")
	}
	after, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, approved.Review.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReviewStatusApproved, after.Status)
}

func TestAnOldBuildIsServedTheBareHoldUnderTheManagedRule(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "old-build", "old-rule",
		`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},`+
			`"medium":{"action":"require_approval","approver":"human","approval_groups":["dba"]},"low":{"action":"warn"}}`)

	// A build without the object form reads the bare hold and the rule that
	// carries the level groups.
	w := handshake(t, sc, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var served daemon.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	la := served.Listeners[0].Analyzer
	require.NotNil(t, la)
	assert.Equal(t, "require_review", la.HighRisk.Action)
	assert.Equal(t, "require_review", la.MediumRisk.Action)
	assert.Equal(t, "warn", la.LowRisk.Action)
	assert.Equal(t, "old-rule", la.ApprovalRule)
	assert.NotContains(t, w.Body.String(), `"approver"`)
	approval, err := services.AnalyzerApprovalReviewers(models.DB, uuid.MustParse(statusTestOrgID), "old-rule")
	require.NoError(t, err)
	assert.Equal(t, []string{"dba"}, approval)

	// The old build files under the rule it was served, as before the object
	// form; a level group member approves and the claim releases it.
	legacy := func(statement string) *httptest.ResponseRecorder {
		t.Helper()
		row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
		require.NoError(t, err)
		return postLevelBody(row, statement, map[string]any{"approval_rule": "old-rule"})
	}
	rec := legacy("DELETE FROM payouts WHERE id = 1")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	require.NotNil(t, filed.Review.AccessRequestRuleName)
	assert.Equal(t, "old-rule", *filed.Review.AccessRequestRuleName)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	claim := decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID))
	assert.True(t, claim.Forward, "the approval releases the statement the old build held")

	// A build with it reads the levels as written.
	w = handshake(t, sc, daemon.CapabilityRiskLevelObject)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"approver":"human"`)
	assert.NotContains(t, w.Body.String(), `"approval_rule"`)
	// That build was served object levels, so the rule authorizes nothing.
	rec = legacy("DELETE FROM payouts WHERE id = 2")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	// An agent level has no bare form: the old build is refused, and the
	// refusal is recorded with its reason.
	setLevelSpec(t, "old-rule", `{"high":{"action":"require_approval","approver":"ai_agent"}}`)
	w = handshake(t, sc, "")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), daemon.CapabilityRiskLevelObject)
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastOutcome)
	assert.Equal(t, models.SidecarOutcomeNotServed, *row.LastOutcome)
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, daemon.CapabilityRiskLevelObject)
	// A build the plane does not serve files nothing.
	rec = legacy("DELETE FROM payouts WHERE id = 3")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	// The build with the capability is still served.
	w = handshake(t, sc, daemon.CapabilityRiskLevelObject)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// The managed rule is editable through the access request API. An old build
// files under it, so it is served only while the rule grants what the levels
// grant; else the plane refuses that build, naming the capability.
func TestAnOldBuildIsRefusedWhenTheManagedRuleGrantsMore(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	for name, edit := range map[string]func(*models.AccessRequestRule){
		"force":     func(r *models.AccessRequestRule) { r.ForceApprovalGroups = []string{"security"} },
		"reviewers": func(r *models.AccessRequestRule) { r.ReviewersGroups = []string{"dba", "payments"} },
		"minimum": func(r *models.AccessRequestRule) {
			r.ReviewersGroups = []string{"dba", "payments"}
			two := 2
			r.MinApprovals = &two
		},
		"all groups": func(r *models.AccessRequestRule) { r.AllGroupsMustApprove = true },
	} {
		ruleName := "edited-" + strings.ReplaceAll(name, " ", "-")
		sc := seedLevelSidecar(t, ruleName, ruleName,
			`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`)
		require.Equal(t, http.StatusOK, handshake(t, sc, "").Code, "%s: the untouched rule serves", name)

		rule, err := services.AnalyzerApprovalRule(models.DB, org, ruleName)
		require.NoError(t, err)
		require.NotNil(t, rule)
		edit(rule)
		require.NoError(t, models.UpdateAccessRequestRule(models.DB, rule))

		w := handshake(t, sc, "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "%s: %s", name, w.Body.String())
		assert.Contains(t, w.Body.String(), daemon.CapabilityRiskLevelObject, name)
		row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
		require.NoError(t, err)
		rec := postLevelBody(row, "DELETE FROM t", map[string]any{"approval_rule": ruleName})
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: an old build files nothing: %s", name, rec.Body.String())

		// A build that reads the object form never uses the rule.
		assert.Equal(t, http.StatusOK, handshake(t, sc, daemon.CapabilityRiskLevelObject).Code, name)
	}
}

// An approval_rule a listener keeps from its own block is inherited by the
// bound rule's block. Beside object levels it releases nothing, and a filing
// under it must not let its groups approve.
func TestAnInheritedApprovalRuleDoesNotReleaseAnObjectLevel(t *testing.T) {
	startStatusTestDB(t)
	seedApprovalRule(t)
	for name, spec := range map[string]string{
		"inherit-human": `{"high":{"action":"require_approval","approver":"human","approval_groups":["sre"]}}`,
		"inherit-agent": `{"high":{"action":"require_approval","approver":"ai_agent"}}`,
	} {
		sc := seedLevelSidecar(t, name, name+"-rule", spec)
		stored := daemon.Config(sc.Configuration)
		stored.Listeners[0].Analyzer = &daemon.LaneAnalyzerConfig{ApprovalRule: "payments-approvers"}
		row, err := models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, models.SidecarConfiguration(stored))
		require.NoError(t, err)
		// A build that reads the object form, so nothing is downgraded.
		row.Capabilities = []string{daemon.CapabilityRiskLevelObject}

		served, err := services.ServedSidecarConfiguration(models.DB, row)
		require.NoError(t, err)
		require.Equal(t, "payments-approvers", served.Listeners[0].Analyzer.ApprovalRule, "%s: the rule is inherited", name)

		rec := postLevelBody(row, "DELETE FROM t", map[string]any{"approval_rule": "payments-approvers"})
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
	}
}
