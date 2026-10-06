package reviewapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const decisionTestOrgID = "00000000-0000-0000-0000-0000000000c7"

func startDecisionTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("close embedded database: %v", err)
		}
	})
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'review-decision-test')`, decisionTestOrgID).Error)
}

// seedApprovedSidecarReview files a sidecar review a dba approved.
func seedApprovedSidecarReview(t *testing.T) *models.Review {
	t.Helper()
	sc := &models.Sidecar{OrgID: decisionTestOrgID, Name: "sc-" + uuid.NewString()[:8],
		KeyHash: models.HashAPIKey(uuid.NewString()), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	sessionID := uuid.NewString()
	statement := "DELETE FROM users WHERE id = '" + sessionID + "';"
	reviewedAt := time.Now().UTC().Add(-time.Minute)
	rev := &models.Review{
		ID:                    uuid.NewString(),
		OrgID:                 decisionTestOrgID,
		Type:                  models.ReviewTypeOneTime,
		Status:                models.ReviewStatusPending,
		SessionID:             sessionID,
		SidecarID:             sql.NullString{String: sc.ID, Valid: true},
		ListenerName:          sql.NullString{String: "appdb", Valid: true},
		StatementHash:         sql.NullString{String: models.HashStatement([]byte(statement)), Valid: true},
		OwnerID:               sc.ID,
		OwnerEmail:            "hoop@hoop.dev",
		AccessRequestRuleName: ptr.String("payments-approvers"),
		MinApprovals:          ptr.Int(1),
		CreatedAt:             time.Now().UTC(),
	}
	rev.ReviewGroups = []models.ReviewGroups{{ID: uuid.NewString(), OrgID: decisionTestOrgID, ReviewID: rev.ID,
		GroupName: "dba", Status: models.ReviewStatusApproved, ReviewedAt: &reviewedAt}}
	sess := models.Session{ID: sessionID, OrgID: decisionTestOrgID, BlobInput: models.BlobInputType(statement),
		ConnectionType: "custom", Verb: "exec", Status: "open", UserID: sc.ID, UserName: sc.Name,
		UserEmail: "hoop@hoop.dev", CreatedAt: time.Now().UTC()}
	require.NoError(t, models.CreateSidecarReview(models.DB, sess, rev, statement))
	require.NoError(t, models.UpdateReviewStatus(decisionTestOrgID, rev.ID, models.ReviewStatusApproved))

	got, err := models.GetReviewByIdOrSid(decisionTestOrgID, rev.ID)
	require.NoError(t, err)
	return got
}

// The decision reads APPROVED, the sidecar claims in between, then the write
// runs. The customer's symptom was success on a statement that already ran.
func TestPersistDecisionLosesToTheClaim(t *testing.T) {
	startDecisionTestDB(t)
	admin := newFakeContext(uuid.NewString(), "admin@hoop.dev", []string{types.GroupAdmin})
	// The API sets it from the session; the appended revoker row carries it.
	admin.OrgID = decisionTestOrgID

	t.Run("a revoke read before the claim answers wrong state", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t)
		fromStatus := rev.Status

		decided, err := doReview(admin, rev, nil, models.ReviewStatusRevoked, false)
		require.NoError(t, err)

		claimed, _, err := models.ClaimApprovedSidecarReview(models.DB, decisionTestOrgID, rev.ID)
		require.NoError(t, err)
		require.True(t, claimed)

		assert.Equal(t, ErrWrongState, persistDecision(decided, fromStatus))
		got, err := models.GetReviewByIdOrSid(decisionTestOrgID, rev.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusExecuted, got.Status)
		assert.Len(t, got.ReviewGroups, 1, "a lost decision writes no group row")
	})

	t.Run("a revoke with no claim is written", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t)
		fromStatus := rev.Status

		decided, err := doReview(admin, rev, nil, models.ReviewStatusRevoked, false)
		require.NoError(t, err)

		require.NoError(t, persistDecision(decided, fromStatus))
		got, err := models.GetReviewByIdOrSid(decisionTestOrgID, rev.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusRevoked, got.Status)
	})
}
