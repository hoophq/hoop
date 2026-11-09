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

// handoffSpec hands every level off to the AI agent, which may select
// require_approval. Its hold goes to dba on high, to the administrators on
// medium, and to the approval agent on low.
func handoffSpec(ruleName string) string {
	return `{"high": "ai-handoff", "medium": "ai-handoff", "low": "ai-handoff",
		"approval_rule": "` + ruleName + `",
		"handoffs": {
			"high": {"allowed_actions": ["allow", "block", "require_approval"]},
			"medium": {"allowed_actions": ["warn", "require_approval"]},
			"low": {"allowed_actions": ["allow", "block", "require_approval"]}},
		"approvers": {
			"high": {"type": "human", "groups": ["dba"]},
			"medium": {"type": "human"},
			"low": {"type": "ai_agent"}}}`
}

// handoffCaps are the capabilities of a build that reads hand-off levels and
// level approvers.
const handoffCaps = daemon.CapabilityLevelApprovers + "," + daemon.CapabilityAIHandoff

func fileHandoff(sc *models.Sidecar, statement, level string) *httptest.ResponseRecorder {
	return postLevelBody(sc, statement, map[string]any{"risk_level": level, "handoff": true})
}

func TestAHandoffHoldIsAuthorizedByTheLevelApprover(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	sc := seedLevelSidecar(t, "handoff", "handoff-rule", handoffSpec("handoff-rule"))

	// A rule that holds only through hand-offs still gets its managed rule.
	managed, err := services.AnalyzerApprovalRule(models.DB, org, "handoff-rule")
	require.NoError(t, err)
	require.NotNil(t, managed, "a hand-off that may hold keeps the managed rule")

	// high: dba approves, one approval, filed through the hand-off.
	rec := fileHandoff(sc, "DELETE FROM payments WHERE id = 1", "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	assert.False(t, filed.Forward)
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	require.NotNil(t, filed.Review.MinApprovals)
	assert.Equal(t, 1, *filed.Review.MinApprovals)
	assert.Nil(t, filed.Review.AccessRequestRuleName)
	require.NotNil(t, filed.Review.RiskLevel)
	assert.Equal(t, "high", *filed.Review.RiskLevel)
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

	// The limits of the block's approval_rule apply to the hand-off review.
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, org, "handoff-rule", &pending, nil))
	timed := decodeReviewResponse(t, fileHandoff(sc, "DELETE FROM payments WHERE id = 3", "high"))
	require.NotNil(t, timed.Review.ExpiresAt, "the hand-off review takes the approval rule's pending limit")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), *timed.Review.ExpiresAt, time.Minute)
}

// A hand-off filing needs an ai-handoff level that allows require_approval
// and names a human approver. A hand-off level with no approvers entry files
// a bare hold under approval_rule, as a direct bare hold does.
func TestAHandoffFilingThatDoesNotMatchTheLevelIsRefused(t *testing.T) {
	startStatusTestDB(t)
	// high holds directly, medium hands off with no hold, low hands off a
	// human hold.
	sc := seedLevelSidecar(t, "handoff-mismatch", "handoff-mismatch-rule", `{
		"high": "require_approval", "medium": "ai-handoff", "low": "ai-handoff",
		"approval_rule": "handoff-mismatch-rule",
		"handoffs": {"medium": {"allowed_actions": ["allow", "block"]},
			"low": {"allowed_actions": ["require_approval"]}},
		"approvers": {"high": {"type": "human"}, "low": {"type": "human"}}}`)
	agent := seedLevelSidecar(t, "handoff-agent", "handoff-agent-rule", handoffSpec("handoff-agent-rule"))
	bare := seedLevelSidecar(t, "handoff-bare", "handoff-bare-rule", `{"high": "ai-handoff",
		"approval_rule": "handoff-bare-rule",
		"handoffs": {"high": {"allowed_actions": ["allow", "require_approval"]}}}`)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a hand-off filing on a direct level":       fileHandoff(sc, "DELETE FROM t", "high"),
		"a hand-off filing on a level with no hold": fileHandoff(sc, "DELETE FROM t", "medium"),
		"a direct filing on a hand-off level":       fileLevel(sc, "DELETE FROM t", "low"),
		"a hand-off hold for the approval agent":    fileHandoff(agent, "DELETE FROM t", "low"),
		"a hand-off hold with no approvers entry":   fileHandoff(bare, "DELETE FROM t", "high"),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "is not configured to hold the", name)
	}

	// The hand-off agent's human hold files.
	rec := fileHandoff(sc, "DELETE FROM t", "low")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// The bare hand-off hold files under the block's approval_rule.
	rec = postLevelBody(bare, "DELETE FROM t", map[string]any{"approval_rule": "handoff-bare-rule"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	require.NotNil(t, filed.Review.AccessRequestRuleName)
	assert.Equal(t, "handoff-bare-rule", *filed.Review.AccessRequestRuleName)
	assert.False(t, filed.Review.ViaHandoff)

	// handoff is read only with a risk level.
	rec = postLevelBody(bare, "DELETE FROM t", map[string]any{"approval_rule": "handoff-bare-rule", "handoff": true})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

// A level can switch between a direct hold and a hand-off. A review filed one
// way never answers a filing made the other way.
func TestAHandoffReviewIsKeptApartFromADirectReview(t *testing.T) {
	startStatusTestDB(t)
	spec := handoffSpec("handoff-apart-rule")
	sc := seedLevelSidecar(t, "handoff-apart", "handoff-apart-rule", spec)
	const statement = "DELETE FROM payments WHERE id = 9"

	handoff := decodeReviewResponse(t, fileHandoff(sc, statement, "high"))
	assert.True(t, handoff.Review.ViaHandoff)

	setLevelSpec(t, "handoff-apart-rule", `{"high": "require_approval", "approval_rule": "handoff-apart-rule",
		"approvers": {"high": {"type": "human", "groups": ["dba"]}}}`)
	rec := fileLevel(sc, statement, "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	direct := decodeReviewResponse(t, rec)
	assert.NotEqual(t, handoff.Review.ID, direct.Review.ID, "a direct filing files its own review")
	assert.False(t, direct.Review.ViaHandoff)

	setLevelSpec(t, "handoff-apart-rule", spec)
	again := decodeReviewResponse(t, fileHandoff(sc, statement, "high"))
	assert.Equal(t, handoff.Review.ID, again.Review.ID, "the hand-off filing answers from its own review")
}

// No build before the hand-off can run an ai-handoff level, so the plane
// refuses to serve it one, at the handshake and when a rule is saved.
func TestAnOldBuildIsRefusedAHandoffLevel(t *testing.T) {
	startStatusTestDB(t)
	spec := handoffSpec("handoff-old-rule")
	sc := seedLevelSidecar(t, "handoff-old", "handoff-old-rule", spec)

	for _, caps := range []string{"", daemon.CapabilityLevelApprovers} {
		w := handshake(t, sc, caps)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "caps %q: %s", caps, w.Body.String())
		assert.Contains(t, w.Body.String(), daemon.CapabilityAIHandoff, "caps %q", caps)
	}
	w := handshake(t, sc, handoffCaps)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"ai-handoff"`)
	assert.Contains(t, w.Body.String(), `"handoffs"`)

	// Save time: a build that handshook without the capability is bound to
	// the rule being saved.
	_, err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.191.0", "", "", "", "",
		[]string{daemon.CapabilityLevelApprovers})
	require.NoError(t, err)
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	row.Configuration.Analyzer = &daemon.AnalyzerConfig{Provider: "anthropic", Model: "m"}
	_, err = models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, row.Configuration)
	require.NoError(t, err)
	err = services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"handoff-old-rule", "handoff-old-rule", json.RawMessage(spec),
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), daemon.CapabilityAIHandoff)

	// The build that reports it is saved for.
	_, err = models.RecordSidecarHandshake(models.DB, sc.ID, "1.191.0", "", "", "", "",
		[]string{daemon.CapabilityLevelApprovers, daemon.CapabilityAIHandoff})
	require.NoError(t, err)
	require.NoError(t, services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"handoff-old-rule", "handoff-old-rule", json.RawMessage(spec),
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}))
}
