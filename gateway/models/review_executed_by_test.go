package models_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// NB12: PROCESSING and the executor are written together, and the session
// details read the executor back.
func TestMarkReviewProcessing(t *testing.T) {
	startTestDB(t)

	const sid = "00000000-0000-0000-0000-000000000e01"
	require.NoError(t, models.UpsertSession(models.Session{ID: sid, OrgID: testOrgID, Connection: "pg-prod",
		ConnectionType: "database", Verb: "exec", Status: "ready", CreatedAt: time.Now().UTC()}))
	rev := &models.Review{ID: "00000000-0000-0000-0000-000000000e02", OrgID: testOrgID, SessionID: sid,
		Type: models.ReviewTypeOneTime, Status: models.ReviewStatusApproved, ConnectionName: "pg-prod",
		OwnerID: "owner", OwnerEmail: "owner@example.com", CreatedAt: time.Now().UTC()}
	require.NoError(t, models.CreateReview(rev, "SELECT 1"))

	executedBy := func() *string {
		t.Helper()
		var value *string
		require.NoError(t, models.DB.Raw(`SELECT executed_by FROM private.reviews WHERE id = ?`, rev.ID).Row().Scan(&value))
		return value
	}
	require.Nil(t, executedBy(), "a new review has no executor")

	require.NoError(t, models.MarkReviewProcessing(models.DB, testOrgID, rev.ID, "admin@example.com"))
	got, err := models.GetReviewByIdOrSid(testOrgID, rev.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewStatusProcessing, got.Status)
	require.Equal(t, "admin@example.com", *executedBy())

	sess, err := models.GetSessionByID(testOrgID, sid)
	require.NoError(t, err)
	require.NotNil(t, sess.Review)
	require.Equal(t, "admin@example.com", *sess.Review.ExecutedBy)

	// A run with no email stores no executor.
	require.NoError(t, models.MarkReviewProcessing(models.DB, testOrgID, rev.ID, ""))
	require.Nil(t, executedBy())

	err = models.MarkReviewProcessing(models.DB, testOrgID, "00000000-0000-0000-0000-000000000e09", "admin@example.com")
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound), "got %v", err)
}
