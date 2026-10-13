package apisidecar

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedMirroredSidecar stores a sidecar whose appdb listener files reviews
// under payments-approvers and has a mirror connection named <sidecar>-appdb.
func seedMirroredSidecar(t *testing.T, name string) (*models.Sidecar, *models.SidecarMirror) {
	t.Helper()

	sc := &models.Sidecar{
		OrgID:     statusTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name:     "appdb",
			Protocol: "postgres",
			Analyzer: &daemon.LaneAnalyzerConfig{ApprovalRule: "payments-approvers"},
		}}},
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		return services.SyncSidecarListenerConnectionsTx(tx, sc)
	}))
	mirror, err := models.GetSidecarMirror(models.DB, statusTestOrgID, sc.ID, "appdb")
	require.NoError(t, err)
	require.Equal(t, name+"-appdb", mirror.Name)
	return sc, mirror
}

// seedApprovalRule stores the payments-approvers rule when the database has
// none yet.
func seedApprovalRule(t *testing.T) { seedApprovalRuleIn(t, statusTestOrgID) }

func seedApprovalRuleIn(t *testing.T, orgID string) {
	t.Helper()
	_, err := models.GetAccessRequestRuleByName(models.DB, "payments-approvers", uuid.MustParse(orgID))
	if err == nil {
		return
	}
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, models.CreateAccessRequestRule(models.DB, &models.AccessRequestRule{
		OrgID:                  uuid.MustParse(orgID),
		Name:                   "payments-approvers",
		AccessType:             models.AccessTypeSidecar,
		ReviewersGroups:        pq.StringArray{"dba"},
		MinApprovals:           ptr.Int(1),
		ConnectionNames:        pq.StringArray{},
		ApprovalRequiredGroups: pq.StringArray{},
		ForceApprovalGroups:    pq.StringArray{},
		SkipReviewGroups:       pq.StringArray{},
	}))
}

// A held statement shows the resource it came from: the review, its session
// and the Slack message all name the listener's mirror, and the approval
// still takes the sidecar path (no access window, claimable once).
func TestSidecarReviewRecordsTheMirror(t *testing.T) {
	startStatusTestDB(t)
	seedApprovalRule(t)
	sc, mirror := seedMirroredSidecar(t, "edge")

	rec := postReview(sc, []byte("DELETE FROM users WHERE id = 7;"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var filed openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &filed))

	rev, err := models.GetReviewByIdOrSid(statusTestOrgID, filed.Review.ID)
	require.NoError(t, err)
	assert.Equal(t, mirror.Name, rev.ConnectionName)
	assert.Equal(t, mirror.ID, rev.ConnectionID.String)
	assert.Equal(t, "appdb", rev.ListenerName.String, "the listener still decides the sidecar path")

	sess, err := models.GetSessionByID(statusTestOrgID, rev.SessionID)
	require.NoError(t, err)
	assert.Equal(t, mirror.Name, sess.Connection)

	assert.Equal(t, mirror.Name, newSlackReviewRequest(sc, rev, "appdb", "x").Connection)

	approved := putReview(t, rev.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, approved.Code, approved.Body.String())
	var body openapi.Review
	require.NoError(t, json.Unmarshal(approved.Body.Bytes(), &body))
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), body.Status)
	assert.Nil(t, body.RevokeAt, "a sidecar review carries no access window")
	if assert.NotNil(t, body.Connection) {
		assert.Equal(t, mirror.Name, body.Connection.Name)
		assert.Equal(t, mirror.ID, body.Connection.ID)
	}

	claim := claimSidecarReview(sc, rev.ID)
	require.Equal(t, http.StatusOK, claim.Code, claim.Body.String())
	var claimed openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(claim.Body.Bytes(), &claimed))
	assert.True(t, claimed.Forward)
}

// A listener with no mirror files as before: nothing stands in for the
// connection, and the Slack message names the listener.
func TestSidecarReviewWithoutAMirror(t *testing.T) {
	startStatusTestDB(t)
	sc := seedReviewingSidecar(t, "plain")

	rec := postReview(sc, []byte("DELETE FROM users WHERE id = 8;"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var filed openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &filed))

	rev, err := models.GetReviewByIdOrSid(statusTestOrgID, filed.Review.ID)
	require.NoError(t, err)
	assert.Empty(t, rev.ConnectionName)
	assert.False(t, rev.ConnectionID.Valid)
	assert.Equal(t, "appdb", newSlackReviewRequest(sc, rev, "appdb", "x").Connection)
}
