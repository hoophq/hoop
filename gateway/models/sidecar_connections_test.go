package models_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

type mirrorRow struct {
	ID       string `gorm:"column:id"`
	Name     string `gorm:"column:name"`
	Type     string `gorm:"column:type"`
	SubType  string `gorm:"column:subtype"`
	Listener string `gorm:"column:sidecar_listener"`
	Tags     string `gorm:"column:tags"`
	ResType  string `gorm:"column:res_type"`
	Bindings int    `gorm:"column:bindings"`
}

// mirrorsOf reads what the sync left for one sidecar, with what hangs off
// each mirror: its resource, its legacy tags and its guardrail bindings.
func mirrorsOf(t *testing.T, sidecarID string) map[string]mirrorRow {
	t.Helper()
	var rows []mirrorRow
	err := models.DB.Raw(`
	SELECT c.id, c.name, c.type, c.subtype, c.sidecar_listener,
		COALESCE(array_to_string(c._tags, ','), '') AS tags,
		r.type::text AS res_type,
		(SELECT count(*) FROM private.guardrail_rules_connections g WHERE g.connection_id = c.id) AS bindings
	FROM private.connections c
	JOIN private.resources r ON r.org_id = c.org_id AND r.name = c.resource_name
	WHERE c.org_id = ? AND c.sidecar_id = ?`, testOrgID, sidecarID).Scan(&rows).Error
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]mirrorRow{}
	for _, r := range rows {
		out[r.Listener] = r
	}
	return out
}

func sync(t *testing.T, sc *models.Sidecar, listeners ...daemon.ListenerConfig) error {
	t.Helper()
	sc.Configuration = models.SidecarConfiguration{Listeners: listeners}
	mirrors, err := services.ProjectListeners(testOrgID, sc)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return models.DB.Transaction(func(tx *gorm.DB) error {
		return models.SyncSidecarConnectionsTx(tx, testOrgID, sc.ID, mirrors)
	})
}

func TestSyncSidecarConnections(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "pay")
	appdb := daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"}
	api := daemon.ListenerConfig{Name: "api", Protocol: "http"}

	if err := sync(t, sc, appdb, api); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	first := mirrorsOf(t, sc.ID)
	if len(first) != 2 || first["appdb"].Name != "pay-appdb" || first["api"].Type != "httpproxy" || first["appdb"].ResType != "database" {
		t.Fatalf("want a mirror per listener with its resource, got %+v", first)
	}

	// What an admin attaches to a mirror: a guardrail binding and tags.
	ruleID := uuid.NewString()
	execSQL(t, `INSERT INTO private.guardrail_rules (id, org_id, name, input, output) VALUES (?, ?, 'deny-drop', '{}', '{}')`, ruleID, testOrgID)
	execSQL(t, `INSERT INTO private.guardrail_rules_connections (org_id, rule_id, connection_id) VALUES (?, ?, ?)`, testOrgID, ruleID, first["appdb"].ID)
	execSQL(t, `UPDATE private.connections SET _tags = '{prod}' WHERE id = ?`, first["appdb"].ID)

	t.Run("a listener that stays keeps its row", func(t *testing.T) {
		appdb.Protocol = "mysql"
		if err := sync(t, sc, appdb, api); err != nil {
			t.Fatal(err)
		}
		got := mirrorsOf(t, sc.ID)["appdb"]
		if got.ID != first["appdb"].ID {
			t.Fatalf("the mirror was recreated: %s -> %s", first["appdb"].ID, got.ID)
		}
		if got.SubType != "mysql" || got.ResType != "database" {
			t.Errorf("the protocol change did not reach the row: %+v", got)
		}
		if got.Bindings != 1 || got.Tags != "prod" {
			t.Errorf("what was attached to the mirror is gone: %+v", got)
		}
	})

	t.Run("a listener that goes takes its mirror and resource", func(t *testing.T) {
		if err := sync(t, sc, appdb); err != nil {
			t.Fatal(err)
		}
		got := mirrorsOf(t, sc.ID)
		if _, still := got["api"]; still || len(got) != 1 {
			t.Fatalf("want only appdb left, got %+v", got)
		}
		if n := queryString(t, `SELECT count(*)::text FROM private.resources WHERE org_id = ? AND name = 'pay-api'`, testOrgID); n != "0" {
			t.Errorf("the resource of the removed listener stayed")
		}
	})

	t.Run("the same input is a no-op", func(t *testing.T) {
		before := mirrorsOf(t, sc.ID)
		if err := sync(t, sc, appdb); err != nil {
			t.Fatal(err)
		}
		after := mirrorsOf(t, sc.ID)
		if before["appdb"].ID != after["appdb"].ID || after["appdb"].Bindings != 1 {
			t.Errorf("a no-op sync changed the mirror: %+v -> %+v", before, after)
		}
	})

	t.Run("a name another connection has is refused, whole", func(t *testing.T) {
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'pay-cache', 'custom', 'redis')`, testOrgID)
		execSQL(t, `INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'pay-cache', 'custom', 'redis', 'pay-cache')`, testOrgID)
		err := sync(t, sc, appdb, daemon.ListenerConfig{Name: "logs", Protocol: "clickhouse"}, daemon.ListenerConfig{Name: "cache", Protocol: "clickhouse"})
		var taken models.ErrSidecarConnectionNameTaken
		if !errors.As(err, &taken) || taken.Listener != "cache" || taken.Name != "pay-cache" {
			t.Fatalf("want ErrSidecarConnectionNameTaken for cache, got %v", err)
		}
		if got := mirrorsOf(t, sc.ID); len(got) != 1 {
			t.Errorf("the refused sync left a partial result: %+v", got)
		}
		var foreign struct {
			SidecarID sql.NullString `gorm:"column:sidecar_id"`
			ManagedBy sql.NullString `gorm:"column:managed_by"`
		}
		models.DB.Raw(`SELECT sidecar_id, managed_by FROM private.connections WHERE org_id = ? AND name = 'pay-cache'`, testOrgID).Scan(&foreign)
		if foreign.SidecarID.Valid || foreign.ManagedBy.Valid {
			t.Errorf("the admin's connection was taken over: %+v", foreign)
		}
	})

	t.Run("no listeners leaves nothing", func(t *testing.T) {
		if err := sync(t, sc); err != nil {
			t.Fatal(err)
		}
		if got := mirrorsOf(t, sc.ID); len(got) != 0 {
			t.Errorf("want no mirrors, got %+v", got)
		}
		if n := queryString(t, `SELECT count(*)::text FROM private.resources WHERE org_id = ? AND name = 'pay-appdb'`, testOrgID); n != "0" {
			t.Errorf("the mirror's resource stayed")
		}
	})
}

// The sidecar delete goes through the same path, so the resources the FK
// cascade cannot reach are removed with the mirrors.
func TestDeletingASidecarRemovesItsMirrorsAndResources(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "pay")
	if err := sync(t, sc, daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, sc.Name); err != nil {
		t.Fatal(err)
	}
	if n := queryString(t, `SELECT count(*)::text FROM private.connections WHERE org_id = ? AND name = 'pay-appdb'`, testOrgID); n != "0" {
		t.Error("the mirror outlived the sidecar")
	}
	if n := queryString(t, `SELECT count(*)::text FROM private.resources WHERE org_id = ? AND name = 'pay-appdb'`, testOrgID); n != "0" {
		t.Error("the mirror's resource outlived the sidecar")
	}
}
