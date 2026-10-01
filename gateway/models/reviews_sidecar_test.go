package models_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/migrations"
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

// A refusal is final for one review, not for the statement. The resend must
// file a new review, and the index must accept it.
func TestRefusedSidecarReviewIsNotLive(t *testing.T) {
	for _, status := range []models.ReviewStatusType{models.ReviewStatusRejected, models.ReviewStatusRevoked} {
		t.Run(string(status), func(t *testing.T) {
			startTestDB(t)
			sc := seedSidecar(t, "refused")
			const statement = "DELETE FROM x;"
			hash := models.HashStatement([]byte(statement))
			rev := seedSidecarReview(t, sc, statement)

			if err := models.UpdateReviewStatus(testOrgID, rev.ID, status); err != nil {
				t.Fatalf("refuse the review: %v", err)
			}
			_, err := models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb", "payments-approvers", hash)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("the live lookup returned a %s review: err=%v", status, err)
			}

			next := seedSidecarReview(t, sc, statement)
			got, err := models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb", "payments-approvers", hash)
			if err != nil {
				t.Fatalf("the live lookup lost the new review: %v", err)
			}
			if got.ID != next.ID {
				t.Errorf("live review = %s, want the new one %s", got.ID, next.ID)
			}
		})
	}
}

// The down migration restores the old index, which allows one refused row per
// statement. A refiled statement must not block it, and no row is deleted.
func TestRefileMigrationRollsBack(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "rollback")
	const statement = "DELETE FROM x;"
	old := seedSidecarReview(t, sc, statement)
	if err := models.UpdateReviewStatus(testOrgID, old.ID, models.ReviewStatusRejected); err != nil {
		t.Fatalf("reject the review: %v", err)
	}
	next := seedSidecarReview(t, sc, statement)

	down, err := migrations.FS.ReadFile("000125_sidecar_review_refile_refused.down.sql")
	if err != nil {
		t.Fatalf("read the down migration: %v", err)
	}
	if err := models.DB.Exec(string(down)).Error; err != nil {
		t.Fatalf("the down migration failed: %v", err)
	}

	hashes := map[string]sql.NullString{}
	rows, err := models.DB.Raw(`SELECT id, statement_hash FROM private.reviews WHERE id IN (?, ?)`,
		old.ID, next.ID).Rows()
	if err != nil {
		t.Fatalf("read the reviews: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var hash sql.NullString
		if err := rows.Scan(&id, &hash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		hashes[id] = hash
	}
	if len(hashes) != 2 {
		t.Fatalf("got %d reviews after rollback, want 2", len(hashes))
	}
	if hashes[old.ID].Valid {
		t.Errorf("the older review kept its hash")
	}
	if !hashes[next.ID].Valid {
		t.Errorf("the newest review lost its hash")
	}
}

// A second PENDING review for the same statement must still be refused, so
// racing first requests cannot both file.
func TestPendingSidecarReviewBlocksADuplicate(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "duplicate")
	const statement = "DELETE FROM x;"
	first := seedSidecarReview(t, sc, statement)

	dup := *first
	dup.ID = uuid.NewString()
	dup.SessionID = uuid.NewString()
	sess := models.Session{
		ID: dup.SessionID, OrgID: testOrgID, BlobInput: models.BlobInputType(statement),
		ConnectionType: "custom", Verb: "exec", Status: "open",
		UserID: sc.ID, UserName: sc.Name, UserEmail: "hoop@hoop.dev", CreatedAt: time.Now().UTC(),
	}
	err := models.CreateSidecarReview(models.DB, sess, &dup, statement)
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("a second pending review was filed: err=%v", err)
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

// seedApprovedSidecarReview files a sidecar review with one reviewer group and
// approves it, the state a revoke acts on.
func seedApprovedSidecarReview(t *testing.T, sc *models.Sidecar, statement string) *models.Review {
	t.Helper()
	rev := seedSidecarReview(t, sc, statement)
	reviewedAt := time.Now().UTC().Add(-time.Minute)
	approver := "approver@hoop.dev"
	if err := models.DB.Table("private.review_groups").Create(map[string]any{
		"id":          uuid.NewString(),
		"org_id":      testOrgID,
		"review_id":   rev.ID,
		"group_name":  "dba",
		"status":      models.ReviewStatusApproved,
		"owner_email": approver,
		"reviewed_at": reviewedAt,
	}).Error; err != nil {
		t.Fatalf("seed review group: %v", err)
	}
	if err := models.UpdateReviewStatus(testOrgID, rev.ID, models.ReviewStatusApproved); err != nil {
		t.Fatalf("approve the review: %v", err)
	}
	got, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, rev.ID)
	if err != nil {
		t.Fatalf("load the approved review: %v", err)
	}
	return got
}

// revokedByAdmin is what doIndividualReview hands the write for an admin
// outside the reviewer groups: the status moves, and a row is appended.
func revokedByAdmin(rev *models.Review) *models.Review {
	now := time.Now().UTC()
	rev.Status = models.ReviewStatusRevoked
	rev.ReviewGroups = append(rev.ReviewGroups, models.ReviewGroups{
		ID:         uuid.NewString(),
		OrgID:      testOrgID,
		ReviewID:   rev.ID,
		GroupName:  "admin",
		Status:     models.ReviewStatusRevoked,
		OwnerEmail: func() *string { s := "admin@hoop.dev"; return &s }(),
		ReviewedAt: &now,
	})
	return rev
}

func sessionStatus(t *testing.T, sessionID string) string {
	t.Helper()
	var status string
	if err := models.DB.Raw(`SELECT status FROM private.sessions WHERE org_id = ? AND id = ?`,
		testOrgID, sessionID).Scan(&status).Error; err != nil {
		t.Fatalf("read session status: %v", err)
	}
	return status
}

// A decision on a sidecar review is written only while the row still holds the
// status it was made against. The sidecar's claim races it; the loser must not
// label a statement that already ran.
func TestUpdateSidecarReview(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "update-decision")

	t.Run("writes the decision", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM a;")
		if err := models.UpdateSidecarReview(models.DB, revokedByAdmin(rev), models.ReviewStatusApproved); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		got, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, rev.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Status != models.ReviewStatusRevoked {
			t.Errorf("status = %s, want REVOKED", got.Status)
		}
		if len(got.ReviewGroups) != 2 {
			t.Fatalf("review groups = %d, want the approver and the appended revoker", len(got.ReviewGroups))
		}
		if got.RevokedAt != nil {
			t.Errorf("revoked_at = %v, want NULL: it is the gateway's jit expiry", got.RevokedAt)
		}
		if s := sessionStatus(t, rev.SessionID); s != "done" {
			t.Errorf("session status = %q, want done", s)
		}
	})

	t.Run("loses to a claim", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM b;")
		claimed, _, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID)
		if err != nil || !claimed {
			t.Fatalf("claim: claimed=%v err=%v", claimed, err)
		}
		before := sessionStatus(t, rev.SessionID)

		err = models.UpdateSidecarReview(models.DB, revokedByAdmin(rev), models.ReviewStatusApproved)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("a stale decision returned %v, want gorm.ErrRecordNotFound", err)
		}
		got, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, rev.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Status != models.ReviewStatusExecuted {
			t.Errorf("status = %s, want EXECUTED", got.Status)
		}
		if len(got.ReviewGroups) != 1 {
			t.Errorf("review groups = %d, want 1: a lost decision writes no group row", len(got.ReviewGroups))
		}
		if s := sessionStatus(t, rev.SessionID); s != before {
			t.Errorf("session status = %q, want %q", s, before)
		}
	})

	t.Run("leaves another review in the same status alone", func(t *testing.T) {
		other := seedApprovedSidecarReview(t, sc, "DELETE FROM d;")
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM e;")
		before := sessionStatus(t, other.SessionID)
		if err := models.UpdateSidecarReview(models.DB, revokedByAdmin(rev), models.ReviewStatusApproved); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		got, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, other.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Status != models.ReviewStatusApproved || got.SessionID != other.SessionID {
			t.Errorf("other review = %s on session %s, want APPROVED on %s", got.Status, got.SessionID, other.SessionID)
		}
		if s := sessionStatus(t, other.SessionID); s != before {
			t.Errorf("other session status = %q, want %q", s, before)
		}
	})

	t.Run("refuses a review without a listener", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM c;")
		if err := models.DB.Exec(`UPDATE private.reviews SET listener_name = NULL WHERE id = ?`, rev.ID).Error; err != nil {
			t.Fatalf("clear listener: %v", err)
		}
		err := models.UpdateSidecarReview(models.DB, revokedByAdmin(rev), models.ReviewStatusApproved)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("returned %v, want gorm.ErrRecordNotFound", err)
		}
	})
}
