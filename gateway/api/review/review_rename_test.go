package reviewapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A group renamed at the source (a Slack user group handle) while a review
// waits on it must still let its members approve that review (ADR-0019): the
// rename reaches the pending review's groups, not only the rules.
func TestRenamedGroupStillApprovesAPendingReview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))

	const orgID = "00000000-0000-0000-0000-0000000000a9"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'rename-test')`, orgID).Error)
	sc := &models.Sidecar{OrgID: orgID, Name: "payments", KeyHash: models.HashAPIKey("hsc_payments"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	// Ana is in dba, imported from Slack.
	var anaID string
	var group *models.DirectoryGroup
	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		anaID, err = services.UpsertProvisionedUser(tx, orgID, models.ProvisioningSourceSlack, "",
			services.ProvisionedUser{ExternalID: "U-ANA", UserName: "ana@example.com", Active: true})
		if err != nil {
			return err
		}
		if group, err = services.CreateProvisionedGroup(tx, orgID, models.ProvisioningSourceSlack, "dba", "S-DBA"); err != nil {
			return err
		}
		return services.SetGroupMembers(tx, orgID, "dba", []string{anaID})
	}))

	fileReview := func(status models.ReviewStatusType) *models.Review {
		sessionID := uuid.NewString()
		rev := &models.Review{
			ID:           uuid.NewString(),
			OrgID:        orgID,
			Type:         models.ReviewTypeOneTime,
			Status:       status,
			SessionID:    sessionID,
			SidecarID:    sql.NullString{String: sc.ID, Valid: true},
			ListenerName: sql.NullString{String: "appdb", Valid: true},
			OwnerID:      sc.ID,
			OwnerEmail:   "hoop@hoop.dev",
			CreatedAt:    time.Now().UTC(),
			ReviewGroups: []models.ReviewGroups{{ID: uuid.NewString(), OrgID: orgID, GroupName: "dba", Status: status}},
		}
		sess := models.Session{ID: sessionID, OrgID: orgID, BlobInput: "DELETE FROM t", ConnectionType: "custom",
			Verb: "exec", Status: "open", UserID: sc.ID, UserName: sc.Name, UserEmail: "hoop@hoop.dev", CreatedAt: time.Now().UTC()}
		require.NoError(t, models.CreateSidecarReview(models.DB, sess, rev, "DELETE FROM t"))
		return rev
	}
	pending := fileReview(models.ReviewStatusPending)
	settled := fileReview(models.ReviewStatusApproved)

	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		return services.RenameProvisionedGroup(tx, orgID, group, "db-leads")
	}))

	rev, err := models.GetReviewByIdOrSid(orgID, pending.ID)
	require.NoError(t, err)
	require.Len(t, rev.ReviewGroups, 1)
	assert.Equal(t, "db-leads", rev.ReviewGroups[0].GroupName, "the pending review follows the rename")

	old, err := models.GetReviewByIdOrSid(orgID, settled.ID)
	require.NoError(t, err)
	assert.Equal(t, "dba", old.ReviewGroups[0].GroupName, "a settled review keeps the name it was decided under")

	// Ana's groups, as the Slack click would load them, now say db-leads.
	rows, err := models.GetUserGroupsByUserID(anaID)
	require.NoError(t, err)
	var groups []string
	for _, r := range rows {
		groups = append(groups, r.Name)
	}
	approved, err := doReview(newFakeContext("idp|ana", "ana@example.com", groups), rev, nil, models.ReviewStatusApproved, false)
	require.NoError(t, err)
	assert.Equal(t, models.ReviewStatusApproved, approved.Status)
}
