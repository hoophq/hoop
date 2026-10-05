package models_test

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// UpsertConnection enables the default plugins on every connection, and
// CreateOrganization is what creates them. The test org is seeded raw.
func seedDefaultPlugins(t *testing.T) {
	t.Helper()
	for _, name := range []string{plugintypes.PluginAuditName, plugintypes.PluginEditorName,
		plugintypes.PluginSlackName, plugintypes.PluginDLPName, plugintypes.PluginReviewName} {
		execSQL(t, `INSERT INTO private.plugins (org_id, name) VALUES (?, ?) ON CONFLICT DO NOTHING`, testOrgID, name)
	}
}

// mirrorConnection is a connection the way the projection writes it: bound
// to one listener, managed by the sidecar, with no agent.
func mirrorConnection(sc *models.Sidecar, id, listener string) *models.Connection {
	return &models.Connection{
		ID:                 id,
		OrgID:              testOrgID,
		Name:               sc.Name + "-" + listener,
		Type:               "database",
		SubType:            sql.NullString{String: "postgres", Valid: true},
		ManagedBy:          sql.NullString{String: models.ConnectionManagedBySidecar, Valid: true},
		SidecarID:          sql.NullString{String: sc.ID, Valid: true},
		SidecarListener:    sql.NullString{String: listener, Valid: true},
		AccessModeConnect:  "enabled",
		AccessModeExec:     "disabled",
		AccessModeRunbooks: "disabled",
		AccessSchema:       "disabled",
	}
}

func requireBoundTo(t *testing.T, c *models.Connection, sc *models.Sidecar, listener string) {
	t.Helper()
	if c == nil {
		t.Fatal("connection not found")
	}
	if !c.IsSidecarBacked() || c.SidecarID.String != sc.ID || c.SidecarListener.String != listener {
		t.Fatalf("want binding (%s, %s), got sidecar_id=%+v sidecar_listener=%+v",
			sc.ID, listener, c.SidecarID, c.SidecarListener)
	}
}

// An update built from a request carries no binding, and GORM Save writes
// every column. The binding must survive the update paths the API uses, and
// the reads the API feeds them from must carry it.
func TestSidecarBindingSurvivesAConnectionUpdate(t *testing.T) {
	startTestDB(t)
	seedDefaultPlugins(t)
	ctx := models.NewAdminContext(testOrgID)
	sc := seedSidecar(t, "pay")
	const listener = "appdb"
	id := uuid.NewString()

	created, err := models.UpsertConnection(ctx, mirrorConnection(sc, id, listener))
	if err != nil {
		t.Fatalf("create mirror connection: %v", err)
	}
	requireBoundTo(t, created, sc, listener)

	t.Run("reads carry the binding", func(t *testing.T) {
		byName, err := models.GetConnectionByNameOrID(ctx, created.Name)
		if err != nil {
			t.Fatal(err)
		}
		requireBoundTo(t, byName, sc, listener)

		bare, err := models.GetBareConnectionByNameOrID(ctx, id, models.DB)
		if err != nil {
			t.Fatal(err)
		}
		requireBoundTo(t, bare, sc, listener)

		listed, err := models.ListConnections(ctx, models.ConnectionFilterOption{Name: created.Name})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 {
			t.Fatalf("want 1 listed connection, got %d", len(listed))
		}
		requireBoundTo(t, &listed[0], sc, listener)

		page, _, err := models.ListConnectionsPaginated(testOrgID, []string{"admin"},
			models.ConnectionPaginationOption{ConnectionFilterOption: models.ConnectionFilterOption{Name: created.Name}, Page: 1, PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 {
			t.Fatalf("want 1 paginated connection, got %d", len(page))
		}
		requireBoundTo(t, &page[0], sc, listener)
	})

	t.Run("PUT-shaped upsert keeps it", func(t *testing.T) {
		// The struct connections.Put builds: every field from the request,
		// SidecarID and SidecarListener left at their zero value.
		updated, err := models.UpsertConnection(ctx, &models.Connection{
			ID:                 id,
			OrgID:              testOrgID,
			Name:               created.Name,
			Type:               "database",
			SubType:            sql.NullString{String: "postgres", Valid: true},
			ManagedBy:          sql.NullString{},
			Tags:               []string{"edited"},
			AccessModeConnect:  "enabled",
			AccessModeExec:     "disabled",
			AccessModeRunbooks: "disabled",
			AccessSchema:       "disabled",
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		requireBoundTo(t, updated, sc, listener)
		if len(updated.Tags) != 1 || updated.Tags[0] != "edited" {
			t.Errorf("the update itself was lost: tags=%v", updated.Tags)
		}
	})

	t.Run("batch upsert keeps it", func(t *testing.T) {
		batch := []*models.Connection{{
			OrgID:              testOrgID,
			Name:               created.Name,
			ResourceName:       created.ResourceName,
			Type:               "database",
			Status:             models.ConnectionStatusOffline,
			SubType:            sql.NullString{String: "postgres", Valid: true},
			AccessModeConnect:  "enabled",
			AccessModeExec:     "disabled",
			AccessModeRunbooks: "disabled",
			AccessSchema:       "disabled",
		}}
		if err := models.UpsertBatchConnections(models.DB, batch); err != nil {
			t.Fatalf("batch upsert: %v", err)
		}
		after, err := models.GetConnectionByNameOrID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		requireBoundTo(t, after, sc, listener)
	})

	t.Run("half a binding is refused", func(t *testing.T) {
		half := mirrorConnection(sc, uuid.NewString(), "half")
		half.SidecarListener = sql.NullString{}
		if _, err := models.UpsertConnection(ctx, half); err == nil {
			t.Fatal("want the check constraint to refuse sidecar_id without sidecar_listener")
		}
	})

	t.Run("deleting the sidecar deletes the mirror", func(t *testing.T) {
		if _, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, sc.ID); err != nil {
			t.Fatal(err)
		}
		gone, err := models.GetConnectionByNameOrID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if gone != nil {
			t.Fatalf("mirror connection outlived its sidecar: %+v", gone)
		}
	})
}
