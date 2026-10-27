package models_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedCredentialUser creates an active user in groups and returns its subject,
// which is neither its id nor its email, as the request context carries it.
func seedCredentialUser(t *testing.T, name string, groups ...string) string {
	t.Helper()
	subject := "idp|" + name
	execSQL(t, `INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, 'active')`, testOrgID, subject, name+"@hoop.dev", name)
	for _, g := range groups {
		execSQL(t, `INSERT INTO private.user_groups (org_id, user_id, name)
			SELECT ?, id, ? FROM private.users WHERE org_id = ? AND subject = ?`, testOrgID, g, testOrgID, subject)
	}
	return subject
}

func seedCredentialSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{OrgID: testOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

func seedSidecarCredential(t *testing.T, userSubject, sidecarName, lane string, expireAt time.Time) *models.ConnectionCredentials {
	t.Helper()
	key := uuid.NewString()
	cred := &models.ConnectionCredentials{
		ID:             uuid.NewString(),
		OrgID:          testOrgID,
		UserSubject:    userSubject,
		ConnectionName: models.SidecarCredentialConnectionName(sidecarName, lane),
		ConnectionType: models.SidecarCredentialType,
		SecretKeyHash:  models.HashAPIKey(key),
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       expireAt,
		ScramSHA256:    &daemon.SCRAMMaterial{Salt: "c2FsdA==", Iterations: 4096, StoredKey: "c3Q=", ServerKey: "c3Y="},
	}
	require.NoError(t, models.InsertConnectionCredentials(models.DB, cred))
	return cred
}

func TestSidecarCredentialConnectionName(t *testing.T) {
	name := models.SidecarCredentialConnectionName("pay", "appdb")
	assert.Equal(t, "sidecar:pay/appdb", name)
	sc, lane, ok := models.ParseSidecarCredentialConnectionName(name)
	assert.True(t, ok)
	assert.Equal(t, "pay", sc)
	assert.Equal(t, "appdb", lane)

	_, lane, ok = models.ParseSidecarCredentialConnectionName("sidecar:pay/a/b")
	assert.True(t, ok, "a lane may carry a slash")
	assert.Equal(t, "a/b", lane)

	for _, bad := range []string{"pay/appdb", "sidecar:pay", "sidecar:/appdb", "sidecar:pay/"} {
		_, _, ok := models.ParseSidecarCredentialConnectionName(bad)
		assert.False(t, ok, bad)
	}
}

func TestHasSidecarLaneGrant(t *testing.T) {
	startTestDB(t)
	sc := seedCredentialSidecar(t, "grants")
	subject := "jane@hoop.dev"
	group := "dba"
	require.NoError(t, models.CreateSidecarLaneGrant(models.DB, &models.SidecarLaneGrant{
		OrgID: testOrgID, SidecarID: sc.ID, ListenerName: "appdb", Subject: &subject, CreatedBy: "admin"}))
	byGroup := &models.SidecarLaneGrant{
		OrgID: testOrgID, SidecarID: sc.ID, ListenerName: "appdb", GroupName: &group, CreatedBy: "admin"}
	require.NoError(t, models.CreateSidecarLaneGrant(models.DB, byGroup))
	require.NotEmpty(t, byGroup.ID, "the database assigns the id")

	for name, tc := range map[string]struct {
		lane     string
		subjects []string
		groups   []string
		want     bool
	}{
		"by subject":         {"appdb", []string{"id-1", "jane@hoop.dev"}, nil, true},
		"by group":           {"appdb", []string{"id-2", "bob@hoop.dev"}, []string{"sre", "dba"}, true},
		"neither":            {"appdb", []string{"id-2", "bob@hoop.dev"}, []string{"sre"}, false},
		"nothing to match":   {"appdb", nil, nil, false},
		"another lane":       {"api", []string{"jane@hoop.dev"}, []string{"dba"}, false},
		"empty group listed": {"appdb", nil, []string{""}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := models.HasSidecarLaneGrant(models.DB, testOrgID, sc.ID, tc.lane, tc.subjects, tc.groups)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	rows, err := models.ListSidecarLaneGrants(models.DB, testOrgID, sc.ID, "appdb")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, subject, *rows[0].Subject)
	assert.Equal(t, group, *rows[1].GroupName)

	err = models.DeleteSidecarLaneGrant(models.DB, testOrgID, sc.ID, "api", byGroup.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "the lane is part of the key, err=%v", err)
	require.NoError(t, models.DeleteSidecarLaneGrant(models.DB, testOrgID, sc.ID, "appdb", byGroup.ID))
	got, err := models.HasSidecarLaneGrant(models.DB, testOrgID, sc.ID, "appdb", nil, []string{"dba"})
	require.NoError(t, err)
	assert.False(t, got)

	// Refused by the table: a grant must name someone.
	err = models.CreateSidecarLaneGrant(models.DB, &models.SidecarLaneGrant{
		OrgID: testOrgID, SidecarID: sc.ID, ListenerName: "appdb", CreatedBy: "admin"})
	assert.Error(t, err)
}

func TestListValidSidecarCredentials(t *testing.T) {
	startTestDB(t)
	jane := seedCredentialUser(t, "jane", "dba", "sre")
	bob := seedCredentialUser(t, "bob")
	// A row minted for a namesake that was deleted before this sidecar
	// was made.
	stale := seedSidecarCredential(t, jane, "pay", "appdb", time.Now().UTC().Add(time.Hour))
	execSQL(t, `UPDATE private.connection_credentials SET created_at = NOW() - INTERVAL '1 hour' WHERE id = ?`, stale.ID)
	sc := seedCredentialSidecar(t, "pay")
	other := seedCredentialSidecar(t, "pay-2")

	future := time.Now().UTC().Add(time.Hour)
	live := seedSidecarCredential(t, jane, sc.Name, "appdb", future)
	bobs := seedSidecarCredential(t, bob, sc.Name, "api", future)
	seedSidecarCredential(t, jane, sc.Name, "appdb", time.Now().UTC().Add(-time.Minute))
	revoked := seedSidecarCredential(t, jane, sc.Name, "appdb", future)
	require.NoError(t, models.RevokeSidecarCredential(models.DB, testOrgID, revoked.ID))
	// Same prefix, another sidecar: LIKE must not match it.
	seedSidecarCredential(t, jane, other.Name, "appdb", future)

	rows, err := models.ListValidSidecarCredentials(models.DB, testOrgID, mustGetSidecar(t, sc.ID))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, live.ID, rows[0].ID)
	assert.Equal(t, "appdb", rows[0].Lane())
	assert.Equal(t, "idp|jane", rows[0].UserSubject)
	assert.Equal(t, "jane@hoop.dev", rows[0].UserEmail)
	assert.ElementsMatch(t, []string{"dba", "sre"}, []string(rows[0].UserGroups))
	assert.Equal(t, live.SecretKeyHash, rows[0].SecretKeyHash)
	require.NotNil(t, rows[0].ScramSHA256)
	assert.Equal(t, 4096, rows[0].ScramSHA256.Iterations)
	assert.Equal(t, "c2FsdA==", rows[0].ScramSHA256.Salt)
	assert.Equal(t, bobs.ID, rows[1].ID)
	assert.Equal(t, "api", rows[1].Lane())
	assert.Empty(t, rows[1].UserGroups)

	t.Run("a sidecar made after the rows inherits none", func(t *testing.T) {
		execSQL(t, `UPDATE private.sidecars SET created_at = NOW() + INTERVAL '1 hour' WHERE id = ?`, sc.ID)
		rows, err := models.ListValidSidecarCredentials(models.DB, testOrgID, mustGetSidecar(t, sc.ID))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("an inactive user's key drops out", func(t *testing.T) {
		execSQL(t, `UPDATE private.sidecars SET created_at = NOW() - INTERVAL '30 minutes' WHERE id = ?`, sc.ID)
		execSQL(t, `UPDATE private.users SET status = 'inactive' WHERE subject = ?`, bob)
		rows, err := models.ListValidSidecarCredentials(models.DB, testOrgID, mustGetSidecar(t, sc.ID))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, live.ID, rows[0].ID)
		mine, err := models.ListSidecarCredentialsByUser(models.DB, testOrgID, bob)
		require.NoError(t, err)
		assert.Empty(t, mine)
		execSQL(t, `UPDATE private.users SET status = 'active' WHERE subject = ?`, bob)
	})

	t.Run("by user", func(t *testing.T) {
		rows, err := models.ListSidecarCredentialsByUser(models.DB, testOrgID, jane)
		require.NoError(t, err)
		ids := []string{}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		assert.ElementsMatch(t, []string{stale.ID, live.ID, mustCredentialID(t, jane, other.Name)}, ids)
	})

	t.Run("revoke", func(t *testing.T) {
		got, err := models.GetSidecarCredential(models.DB, testOrgID, revoked.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.RevokedAt)
		err = models.RevokeSidecarCredential(models.DB, testOrgID, revoked.ID)
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "revoked twice, err=%v", err)
		_, err = models.GetSidecarCredential(models.DB, testOrgID, uuid.NewString())
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	})
}

func mustGetSidecar(t *testing.T, id string) *models.Sidecar {
	t.Helper()
	sc, err := models.GetSidecarByNameOrID(models.DB, testOrgID, id)
	require.NoError(t, err)
	return sc
}

func mustCredentialID(t *testing.T, userSubject, sidecarName string) string {
	t.Helper()
	var id string
	require.NoError(t, models.DB.Raw(`SELECT id FROM private.connection_credentials
		WHERE org_id = ? AND user_subject = ? AND connection_name LIKE ?`,
		testOrgID, userSubject, "sidecar:"+sidecarName+"/%").Scan(&id).Error)
	return id
}
