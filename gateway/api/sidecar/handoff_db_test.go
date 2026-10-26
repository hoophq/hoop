package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handoffSpec hands every level off to the AI agent. Its hold goes to dba on
// high, to the administrators on medium, and to the approval agent on low.
const handoffSpec = `{
	"high": {"action": "ai-handoff", "allowed_actions": {"allow": {}, "block": {},
		"require_approval": {"approver": "human", "approval_groups": ["dba"]}}},
	"medium": {"action": "ai-handoff", "allowed_actions": {"warn": {}, "require_approval": {"approver": "human"}}},
	"low": {"action": "ai-handoff", "allowed_actions": ["allow", "block", "require_approval"]}}`

func fileHandoff(sc *models.Sidecar, statement, level string) *httptest.ResponseRecorder {
	return postLevelBody(sc, statement, map[string]any{"risk_level": level, "approver": "human", "handoff": true})
}

// seedHandoffSidecar is seedLevelSidecar for a build that also reads
// ai-handoff: the plane refuses a hand-off level to any other build, and
// authorizes a filing against what it serves.
func seedHandoffSidecar(t *testing.T, name, ruleName, spec string) *models.Sidecar {
	t.Helper()
	sc := seedLevelSidecar(t, name, ruleName, spec)
	_, err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.300.0", "", "", "", "",
		[]string{daemon.CapabilityRiskLevelObject, daemon.CapabilityAIHandoff})
	require.NoError(t, err)
	stored, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	return stored
}

func TestAHandoffHoldIsAuthorizedByItsAllowedActions(t *testing.T) {
	startStatusTestDB(t)
	sc := seedHandoffSidecar(t, "handoff", "handoff-rule", handoffSpec)

	// high: dba approves, one approval, filed through the hand-off.
	rec := fileHandoff(sc, "DELETE FROM payments WHERE id = 1", "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	assert.False(t, filed.Forward)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	require.NotNil(t, filed.Review.MinApprovals)
	assert.Equal(t, 1, *filed.Review.MinApprovals)
	require.NotNil(t, filed.Review.RiskLevel)
	assert.Equal(t, "high", *filed.Review.RiskLevel)
	assert.Equal(t, models.ApproverTypeHuman, filed.Review.ApproverType)
	assert.True(t, filed.Review.ViaHandoff)

	// A retry of the same bytes answers from the same review.
	retry := decodeReviewResponse(t, fileHandoff(sc, "DELETE FROM payments WHERE id = 1", "high"))
	assert.Equal(t, filed.Review.ID, retry.Review.ID)

	// The status read and the session say how it was filed.
	statusRec := getReview(sc, filed.Review.ID)
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
	var status openapi.SidecarReviewStatus
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	assert.True(t, status.ViaHandoff)
	stored, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, filed.Review.ID)
	require.NoError(t, err)
	assert.True(t, stored.ViaHandoff)
	sess, err := models.GetSessionByID(statusTestOrgID, stored.SessionID)
	require.NoError(t, err)
	require.NotNil(t, sess.Review)
	assert.True(t, sess.Review.ViaHandoff)

	// Someone outside the groups cannot approve it; a member can, and the
	// claim releases the statement once.
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"sre"})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	rec = putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	claim := decodeReviewResponse(t, claimSidecarReview(sc, filed.Review.ID))
	assert.True(t, claim.Forward)

	// medium names no groups: the administrators approve.
	rec = fileHandoff(sc, "DELETE FROM payments WHERE id = 2", "medium")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	medium := decodeReviewResponse(t, rec)
	assert.Equal(t, []string{types.GroupAdmin}, reviewGroupNames(medium.Review))
	rec = putReview(t, medium.Review.ID, `{"status":"approved"}`, []string{types.GroupAdmin})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The hold inside the hand-off keeps the managed rule, and its limits
	// apply to the hand-off review.
	org := uuid.MustParse(statusTestOrgID)
	reviewers, err := services.AnalyzerApprovalReviewers(models.DB, org, "handoff-rule")
	require.NoError(t, err)
	assert.Equal(t, []string{"dba"}, reviewers, "the first human hold, high first, names the groups")
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, org, "handoff-rule", &pending, nil))
	timed := decodeReviewResponse(t, fileHandoff(sc, "DELETE FROM payments WHERE id = 3", "high"))
	require.NotNil(t, timed.Review.ExpiresAt, "the hand-off review takes the managed rule's pending limit")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), *timed.Review.ExpiresAt, time.Minute)
}

func TestAHandoffFilingThatDoesNotMatchTheLevelIsRefused(t *testing.T) {
	startStatusTestDB(t)
	// high holds directly, medium hands off with no hold, low hands off a
	// human hold.
	sc := seedHandoffSidecar(t, "handoff-mismatch", "handoff-mismatch-rule", `{
		"high": {"action": "require_approval", "approver": "human"},
		"medium": {"action": "ai-handoff", "allowed_actions": ["allow", "block"]},
		"low": {"action": "ai-handoff", "allowed_actions": {"require_approval": {"approver": "human"}}}}`)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a hand-off filing on a direct level":       fileHandoff(sc, "DELETE FROM t", "high"),
		"a hand-off filing on a level with no hold": fileHandoff(sc, "DELETE FROM t", "medium"),
		"a direct filing on a hand-off level":       fileLevel(sc, "DELETE FROM t", "low"),
		"a hand-off agent decision on a human hold": postLevelBody(sc, "DELETE FROM t", map[string]any{
			"risk_level": "low", "approver": "ai_agent", "decision": "approved", "reason": "r", "handoff": true}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "is not configured to hold the", name)
	}

	// The hand-off agent's human hold files.
	rec := fileHandoff(sc, "DELETE FROM t", "low")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// handoff is read only with a risk level.
	rec = postLevelBody(sc, "DELETE FROM t", map[string]any{"approval_rule": "r", "handoff": true})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestAHandoffAgentDecisionIsFiledDecided(t *testing.T) {
	startStatusTestDB(t)
	sc := seedHandoffSidecar(t, "handoff-agent", "handoff-agent-rule", handoffSpec)

	// low's hold goes to the approval agent; a person's filing is refused.
	rec := fileHandoff(sc, "UPDATE users SET plan = 'pro' WHERE id = 7", "low")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = postLevelBody(sc, "UPDATE users SET plan = 'pro' WHERE id = 7", map[string]any{
		"risk_level": "low", "approver": "ai_agent", "decision": "approved", "reason": "One row.", "handoff": true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	decided := decodeReviewResponse(t, rec)
	assert.False(t, decided.Forward)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), decided.Review.Status)
	assert.Equal(t, models.ApproverTypeAIAgent, decided.Review.ApproverType)
	assert.True(t, decided.Review.ViaHandoff)
	require.NotNil(t, decided.Review.DecisionReason)
	assert.Equal(t, "One row.", *decided.Review.DecisionReason)
	assert.Empty(t, decided.Review.ReviewGroupsData)

	stored, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, decided.Review.ID)
	require.NoError(t, err)
	assert.True(t, stored.ViaHandoff)
	assert.False(t, stored.StatementHash.Valid)
	claim := decodeReviewResponse(t, claimSidecarReview(sc, decided.Review.ID))
	assert.False(t, claim.Forward, "the sidecar already acted on the decision")
}

// A level can switch between a direct hold and a hand-off. A review filed one
// way never answers a filing made the other way.
func TestAHandoffReviewIsKeptApartFromADirectReview(t *testing.T) {
	startStatusTestDB(t)
	sc := seedHandoffSidecar(t, "handoff-apart", "handoff-apart-rule", handoffSpec)
	const statement = "DELETE FROM payments WHERE id = 9"

	handoff := decodeReviewResponse(t, fileHandoff(sc, statement, "high"))
	assert.True(t, handoff.Review.ViaHandoff)

	setLevelSpec(t, "handoff-apart-rule",
		`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`)
	rec := fileLevel(sc, statement, "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	direct := decodeReviewResponse(t, rec)
	assert.NotEqual(t, handoff.Review.ID, direct.Review.ID, "a direct filing files its own review")
	assert.False(t, direct.Review.ViaHandoff)

	setLevelSpec(t, "handoff-apart-rule", handoffSpec)
	again := decodeReviewResponse(t, fileHandoff(sc, statement, "high"))
	assert.Equal(t, handoff.Review.ID, again.Review.ID, "the hand-off filing answers from its own review")
}

// No build before the hand-off can run an ai-handoff level, so the plane
// refuses to serve it one, at the handshake and when a rule is saved.
func TestAnOldBuildIsRefusedAHandoffLevel(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "handoff-old", "handoff-old-rule", handoffSpec)

	for _, caps := range []string{"", daemon.CapabilityRiskLevelObject} {
		w := handshake(t, sc, caps)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "caps %q: %s", caps, w.Body.String())
		assert.Contains(t, w.Body.String(), daemon.CapabilityAIHandoff, "caps %q", caps)
	}
	w := handshake(t, sc, daemon.CapabilityRiskLevelObject+","+daemon.CapabilityAIHandoff)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"ai-handoff"`)

	// Save time: a build that handshook without the capability is bound to
	// the rule being saved.
	_, err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.191.0", "", "", "", "",
		[]string{daemon.CapabilityRiskLevelObject})
	require.NoError(t, err)
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	// A filing from that build is refused: it was never served the level.
	rec := fileHandoff(row, "DELETE FROM payments WHERE id = 2", "high")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	row.Configuration.Analyzer = &daemon.AnalyzerConfig{Provider: "anthropic", Model: "m"}
	_, err = models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, row.Configuration)
	require.NoError(t, err)
	err = services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"handoff-old-rule", "handoff-old-rule", json.RawMessage(handoffSpec),
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), daemon.CapabilityAIHandoff)

	// The build that reports it is saved for.
	_, err = models.RecordSidecarHandshake(models.DB, sc.ID, "1.191.0", "", "", "", "",
		[]string{daemon.CapabilityRiskLevelObject, daemon.CapabilityAIHandoff})
	require.NoError(t, err)
	require.NoError(t, services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"handoff-old-rule", "handoff-old-rule", json.RawMessage(handoffSpec),
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}))
}
