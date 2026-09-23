package models_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

func seedSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar %s: %v", name, err)
	}
	return sc
}

func seedSidecarReview(t *testing.T, sc *models.Sidecar, statement string) *models.Review {
	t.Helper()
	sessionID := uuid.NewString()
	rule := "payments-approvers"
	rev := &models.Review{
		ID:                    uuid.NewString(),
		OrgID:                 testOrgID,
		Type:                  models.ReviewTypeOneTime,
		Status:                models.ReviewStatusPending,
		SessionID:             sessionID,
		SidecarID:             sql.NullString{String: sc.ID, Valid: true},
		ListenerName:          sql.NullString{String: "appdb", Valid: true},
		StatementHash:         sql.NullString{String: models.HashStatement([]byte(statement)), Valid: true},
		OwnerID:               sc.ID,
		OwnerEmail:            "hoop@hoop.dev",
		AccessRequestRuleName: &rule,
		CreatedAt:             time.Now().UTC(),
	}
	sess := models.Session{
		ID:             sessionID,
		OrgID:          testOrgID,
		BlobInput:      models.BlobInputType(statement),
		ConnectionType: "custom",
		Verb:           "exec",
		Status:         "open",
		UserID:         sc.ID,
		UserName:       sc.Name,
		UserEmail:      "hoop@hoop.dev",
		CreatedAt:      time.Now().UTC(),
	}
	if err := models.CreateSidecarReview(models.DB, sess, rev, statement); err != nil {
		t.Fatalf("seed sidecar review: %v", err)
	}
	return rev
}

// A sidecar waiting on a review asks about it by id. The lookup must still
// find the row once another connection spent the approval: the live lookup
// skips EXECUTED, and a sidecar that missed the row would file a new review.
func TestGetSidecarReviewFindsASpentReview(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "claim-by-id")
	const statement = "DELETE FROM users WHERE id = 1;"
	rev := seedSidecarReview(t, sc, statement)

	if err := models.UpdateReviewStatus(testOrgID, rev.ID, models.ReviewStatusApproved); err != nil {
		t.Fatalf("approve the review: %v", err)
	}
	claimed, _, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID)
	if err != nil || !claimed {
		t.Fatalf("claim the approval: claimed=%v err=%v", claimed, err)
	}

	_, err = models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb",
		"payments-approvers", models.HashStatement([]byte(statement)))
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("the live lookup returned a spent review: err=%v", err)
	}

	got, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, rev.ID)
	if err != nil {
		t.Fatalf("the lookup by id lost a spent review: %v", err)
	}
	if got.Status != models.ReviewStatusExecuted {
		t.Errorf("status = %s, want EXECUTED", got.Status)
	}
	if got.ListenerName.String != "appdb" {
		t.Errorf("listener = %q, want appdb", got.ListenerName.String)
	}
}

// The token is the authorization. Another sidecar in the same org that
// learns a review id must not be able to read or claim it.
func TestGetSidecarReviewIsScopedToTheSidecar(t *testing.T) {
	startTestDB(t)
	owner := seedSidecar(t, "owner")
	other := seedSidecar(t, "other")
	rev := seedSidecarReview(t, owner, "DELETE FROM orders;")

	_, err := models.GetSidecarReview(models.DB, testOrgID, other.ID, rev.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("another sidecar read the review: err=%v", err)
	}
}
