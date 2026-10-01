package apisidecar

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// putReview runs PUT /reviews/:id as a user in the given groups, through the
// same handler the API mounts.
func putReview(t *testing.T, reviewID, body string, groups []string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := storagev2.NewContext(uuid.NewString(), statusTestOrgID).
		WithUserInfo("Reviewer", "reviewer@hoop.dev", "active", "", groups)
	c.Set(storagev2.ContextKey, ctx)
	c.Params = gin.Params{{Key: "id", Value: reviewID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/reviews/"+reviewID, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	reviewapi.NewHandler(func(orgID, sid, owner, status, reason, rejectedBy string) {}).ReviewByIdOrSid(c)
	return rec
}

func claimSidecarReview(sc *models.Sidecar, reviewID string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	c.Params = gin.Params{{Key: "id", Value: reviewID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews/"+reviewID+"/claim", nil)
	ClaimReview(c)
	return rec
}

// A revoke withdraws an approval the sidecar has not used yet. The customer
// saw 500 and a statement that still ran; the review must end REVOKED, the
// waiting hold must stop, and a resend must file a new review.
func TestPutReviewOnASidecarReview(t *testing.T) {
	startStatusTestDB(t)
	sc := seedStatusSidecar(t, "revoker")

	for name, groups := range map[string][]string{
		"an admin revokes an approved review":                {types.GroupAdmin},
		"a reviewer group member revokes an approved review": {"dba"},
	} {
		t.Run(name, func(t *testing.T) {
			rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
			approvedAt := rev.ReviewGroups[0].ReviewedAt

			rec := putReview(t, rev.ID, `{"status":"revoked"}`, groups)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body openapi.Review
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusRevoked), body.Status)
			assert.Nil(t, body.RevokeAt, "revoked_at is the gateway's jit expiry, not a sidecar decision")

			got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, rev.ID)
			require.NoError(t, err)
			assert.Equal(t, models.ReviewStatusRevoked, got.Status)

			statusRec := getReview(sc, rev.ID)
			require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
			var status openapi.SidecarReviewStatus
			require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
			assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusRevoked), status.Status)
			if assert.NotNil(t, status.DecidedAt) && approvedAt != nil {
				assert.True(t, status.DecidedAt.After(*approvedAt), "decided_at is the revoke, not the approval")
			}

			claimRec := claimSidecarReview(sc, rev.ID)
			require.Equal(t, http.StatusOK, claimRec.Code, claimRec.Body.String())
			var claim openapi.SidecarReviewResponse
			require.NoError(t, json.Unmarshal(claimRec.Body.Bytes(), &claim))
			assert.False(t, claim.Forward, "a revoked approval must not release the statement")
			assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusRevoked), claim.Review.Status)

			_, err = models.GetLiveSidecarReview(models.DB, statusTestOrgID, sc.ID, "appdb",
				"payments-approvers", rev.StatementHash.String)
			assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "a resend must file a new review, err=%v", err)
		})
	}

	for _, status := range []models.ReviewStatusType{
		models.ReviewStatusPending,
		models.ReviewStatusExecuted,
		models.ReviewStatusRejected,
		models.ReviewStatusRevoked,
	} {
		t.Run("a "+string(status)+" review cannot be revoked", func(t *testing.T) {
			rev := seedStatusReview(t, sc, status)
			before := reviewSnapshot(t, rev)

			rec := putReview(t, rev.ID, `{"status":"revoked"}`, []string{types.GroupAdmin})
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"message":"review is in wrong state"}`, rec.Body.String())
			assert.JSONEq(t, before, reviewSnapshot(t, rev))
		})
	}

	t.Run("a revoke after the claim is refused", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
		claimRec := claimSidecarReview(sc, rev.ID)
		require.Equal(t, http.StatusOK, claimRec.Code, claimRec.Body.String())
		before := reviewSnapshot(t, rev)

		rec := putReview(t, rev.ID, `{"status":"revoked"}`, []string{types.GroupAdmin})
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, before, reviewSnapshot(t, rev))
	})

	t.Run("rejecting an approved review still works", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
		rec := putReview(t, rev.ID, `{"status":"rejected","rejection_reason":"not today"}`, []string{types.GroupAdmin})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, rev.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusRejected, got.Status)
		if assert.NotNil(t, got.RejectionReason) {
			assert.Equal(t, "not today", *got.RejectionReason)
		}
	})

	// A row with no listener is a gateway review in a shared database. It keeps
	// the answer it has today: the gateway revokes jit reviews only.
	t.Run("a review without a listener answers as today", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
		require.NoError(t, models.DB.Exec(
			`UPDATE private.reviews SET listener_name = NULL, sidecar_id = NULL, connection_name = 'pg' WHERE id = ?`,
			rev.ID).Error)
		before := reviewSnapshot(t, rev)

		rec := putReview(t, rev.ID, `{"status":"revoked"}`, []string{types.GroupAdmin})
		assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "review not found")
		assert.JSONEq(t, before, reviewSnapshot(t, rev))
	})

	// The claim lands between DoReview's read and its write: the decision must
	// answer wrong state and leave the row as the claim wrote it.
	t.Run("a revoke that loses the race to the claim answers wrong state", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
		const hook = "test:claim-races-the-decision"
		fired := false
		// After DoReview's read and before its write, as the sidecar's claim
		// would land. The claim commits on its own, as it does in production.
		require.NoError(t, models.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
			if fired || !strings.Contains(tx.Statement.SQL.String(), "FROM private.reviews rv") {
				return
			}
			fired = true
			claimed, _, err := models.ClaimApprovedSidecarReview(models.DB, statusTestOrgID, rev.ID)
			require.NoError(t, err)
			require.True(t, claimed)
		}))
		t.Cleanup(func() { _ = models.DB.Callback().Query().Remove(hook) })

		rec := putReview(t, rev.ID, `{"status":"revoked"}`, []string{types.GroupAdmin})
		require.True(t, fired, "the claim did not run between the read and the write")
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"review is in wrong state"}`, rec.Body.String())

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, rev.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusExecuted, got.Status)
		assert.Len(t, got.ReviewGroups, 1, "a lost decision writes no group row")
	})

	t.Run("an approval goes through the conditional write", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusPending)
		rec := putReview(t, rev.ID, `{"status":"approved"}`, []string{"dba"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, rev.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, got.Status)
		var sessionStatus string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.sessions WHERE id = ?`,
			rev.SessionID).Scan(&sessionStatus).Error)
		assert.Equal(t, "ready", sessionStatus)
	})
}
