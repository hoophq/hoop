package apisidecar

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withSidecarResources(t *testing.T, enabled bool) {
	t.Helper()
	featureflag.Set(statusTestOrgID, services.SidecarResourcesFlag, enabled)
	t.Cleanup(func() { featureflag.Set(statusTestOrgID, services.SidecarResourcesFlag, false) })
}

func setListeners(sc *models.Sidecar, listeners ...daemon.ListenerConfig) {
	sc.Configuration = models.SidecarConfiguration{Listeners: listeners}
}

func statusPolicy(t *testing.T) *services.ReviewPolicy {
	t.Helper()
	policy, err := services.ReviewPolicyFromRule(statusTestOrgID, approvalRule())
	require.NoError(t, err)
	return policy
}

func project(t *testing.T, sc *models.Sidecar) error {
	t.Helper()
	return models.DB.Transaction(func(tx *gorm.DB) error { return projectSidecar(tx, sc) })
}

func pgListener(name string) daemon.ListenerConfig {
	return daemon.ListenerConfig{Name: name, Protocol: "postgres", Listen: "127.0.0.1:15432", Upstream: "db:5432"}
}

func TestSidecarResourcesProjection(t *testing.T) {
	startStatusTestDB(t)
	withSidecarResources(t, true)

	sc := seedStatusSidecar(t, "payments")
	setListeners(sc, pgListener("appdb"), daemon.ListenerConfig{Name: "api", Protocol: "http"})
	require.NoError(t, project(t, sc))

	// The sidecar is a resource, and each listener a role under it.
	var res struct {
		Type, Subtype, ManagedBy string
		AgentID                  *string
	}
	require.NoError(t, models.DB.Raw(`
		SELECT type, subtype, managed_by, agent_id FROM private.resources
		WHERE org_id = ? AND name = 'payments'`, statusTestOrgID).Scan(&res).Error)
	assert.Equal(t, "custom", res.Type)
	assert.Equal(t, "sidecar", res.Subtype)
	assert.Equal(t, models.ManagedBySidecar, res.ManagedBy)
	assert.Nil(t, res.AgentID)

	roles, err := models.ListSidecarListenerRoles(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	require.Len(t, roles, 2)
	assert.Equal(t, "payments.api", roles[0].ConnectionName)
	assert.Equal(t, "payments.appdb", roles[1].ConnectionName)
	for _, r := range roles {
		assert.Equal(t, "payments", r.ResourceName)
	}

	var subtype string
	require.NoError(t, models.DB.Raw(`SELECT subtype FROM private.connections WHERE id = ?`,
		roles[1].ConnectionID).Scan(&subtype).Error)
	assert.Equal(t, "postgres", subtype)

	// Idempotent: a second write keeps the same role rows.
	require.NoError(t, project(t, sc))
	again, err := models.ListSidecarListenerRoles(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, roles, again)

	// A listener the configuration dropped takes its role with it.
	setListeners(sc, pgListener("appdb"))
	require.NoError(t, project(t, sc))
	roles, err = models.ListSidecarListenerRoles(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	var count int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.connections WHERE name = 'payments.api'`).
		Scan(&count).Error)
	assert.Zero(t, count)
}

func TestSidecarResourcesRefusesForeignRows(t *testing.T) {
	startStatusTestDB(t)
	withSidecarResources(t, true)

	// A resource written by someone else is never adopted.
	require.NoError(t, models.UpsertResource(models.DB, &models.Resources{
		OrgID: statusTestOrgID, Name: "billing", Type: "database",
		SubType: sql.NullString{String: "postgres", Valid: true},
	}, false))
	sc := seedStatusSidecar(t, "billing")
	setListeners(sc, pgListener("appdb"))

	err := project(t, sc)
	var invalid services.ErrSidecarProjectionInvalid
	require.ErrorAs(t, err, &invalid)
	assert.Contains(t, err.Error(), `resource named "billing"`)
}

func TestSidecarResourcesOffChangesNothing(t *testing.T) {
	startStatusTestDB(t)
	withSidecarResources(t, false)

	sc := seedStatusSidecar(t, "payments")
	setListeners(sc, pgListener("appdb"))
	require.NoError(t, project(t, sc))

	var count int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.resources`).Scan(&count).Error)
	assert.Zero(t, count)

	rev, err := createSidecarReview(sc, "appdb", "DELETE FROM users;", models.HashStatement([]byte("DELETE FROM users;")),
		approvalRule(), statusPolicy(t))
	require.NoError(t, err)
	stored, err := models.GetReviewByIdOrSid(statusTestOrgID, rev.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.ConnectionName)
	assert.False(t, stored.ConnectionID.Valid)
	assert.False(t, stored.ResourceName.Valid)
}

func TestSidecarReviewLinksListenerRole(t *testing.T) {
	startStatusTestDB(t)
	withSidecarResources(t, true)

	sc := seedStatusSidecar(t, "payments")
	setListeners(sc, pgListener("appdb"))
	require.NoError(t, project(t, sc))

	statement := "DELETE FROM users WHERE id = 1;"
	rev, err := createSidecarReview(sc, "appdb", statement, models.HashStatement([]byte(statement)),
		approvalRule(), statusPolicy(t))
	require.NoError(t, err)

	stored, err := models.GetReviewByIdOrSid(statusTestOrgID, rev.ID)
	require.NoError(t, err)
	assert.Equal(t, "payments.appdb", stored.ConnectionName)
	assert.True(t, stored.ConnectionID.Valid)
	assert.Equal(t, "payments", stored.ResourceName.String)
	// The listener binding is still what dedupe and claim key on.
	assert.Equal(t, "appdb", stored.ListenerName.String)
	assert.Equal(t, sc.ID, stored.SidecarID.String)

	var sessConn string
	require.NoError(t, models.DB.Raw(`SELECT connection FROM private.sessions WHERE id = ?`,
		stored.SessionID).Scan(&sessConn).Error)
	assert.Equal(t, "payments.appdb", sessConn)

	// The listing carries the same link.
	list, err := models.ListReviews(statusTestOrgID)
	require.NoError(t, err)
	require.Len(t, *list, 1)
	assert.Equal(t, "payments", (*list)[0].ResourceName.String)

	r := (*list)[0]
	body, err := json.MarshalIndent(map[string]any{
		"sidecar_id": r.SidecarID.String, "listener_name": r.ListenerName.String,
		"connection_name": r.ConnectionName, "connection_id": r.ConnectionID.String,
		"resource_name": r.ResourceName.String, "status": r.Status,
	}, "", "  ")
	require.NoError(t, err)
	t.Logf("review as listed:\n%s", body)
}

func TestSidecarResourcesKeepServedDocument(t *testing.T) {
	startStatusTestDB(t)
	sc := seedStatusSidecar(t, "payments")
	setListeners(sc, pgListener("appdb"))
	cfg, err := models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, sc.Configuration)
	require.NoError(t, err)

	composed := func() string {
		stored, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, cfg.ID)
		require.NoError(t, err)
		doc, err := services.ComposeSidecarConfiguration(models.DB, stored)
		require.NoError(t, err)
		return configRevision(doc)
	}
	before := composed()

	withSidecarResources(t, true)
	require.NoError(t, project(t, cfg))

	// The sidecar is served the same bytes, so it sees no drift.
	assert.Equal(t, before, composed())
}

func TestSidecarResourcesDeleteStorage(t *testing.T) {
	startStatusTestDB(t)
	withSidecarResources(t, true)

	sc := seedStatusSidecar(t, "payments")
	setListeners(sc, pgListener("appdb"))
	require.NoError(t, project(t, sc))

	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.DeleteSidecarStorage(tx, statusTestOrgID, sc.ID); err != nil {
			return err
		}
		_, err := models.DeleteSidecarByNameOrID(tx, statusTestOrgID, sc.ID)
		return err
	}))

	var resources, connections int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.resources`).Scan(&resources).Error)
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.connections`).Scan(&connections).Error)
	assert.Zero(t, resources)
	assert.Zero(t, connections)
}
