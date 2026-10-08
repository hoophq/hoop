package models_test

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func mirrorStatus(t *testing.T, sidecarID string) string {
	t.Helper()
	return queryString(t, `SELECT string_agg(DISTINCT status::text, ',') FROM private.connections WHERE sidecar_id = ?`, sidecarID)
}

// A mirror follows its sidecar's check-ins: online on a handshake, offline
// once the sidecar is silent past SidecarMirrorOfflineAfter. A mirror of
// another sidecar, and a connection with no sidecar, keep their status.
func TestSidecarMirrorStatusFollowsTheLastCheckIn(t *testing.T) {
	startTestDB(t)
	ctx := context.Background()
	pay := seedSidecar(t, "pay")
	ops := seedSidecar(t, "ops")
	for _, sc := range []*models.Sidecar{pay, ops} {
		err := syncMirrors(t, sc, daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"},
			daemon.ListenerConfig{Name: "api", Protocol: "http"})
		if err != nil {
			t.Fatalf("sync %s: %v", sc.Name, err)
		}
	}
	seedAdminConnection(t, "agent-pg", "agent-pg")
	execSQL(t, `UPDATE private.connections SET status = 'online' WHERE name = 'agent-pg'`)

	if got := mirrorStatus(t, pay.ID); got != models.ConnectionStatusOffline {
		t.Fatalf("a new mirror is offline, got %q", got)
	}

	if err := models.RecordSidecarHandshake(models.DB, pay.ID, "1.0.0", "", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := models.MarkSidecarConnectionsOnline(models.DB, testOrgID, pay.ID); err != nil {
		t.Fatal(err)
	}
	if got := mirrorStatus(t, pay.ID); got != models.ConnectionStatusOnline {
		t.Fatalf("every mirror of a sidecar that checked in is online, got %q", got)
	}
	if got := mirrorStatus(t, ops.ID); got != models.ConnectionStatusOffline {
		t.Fatalf("another sidecar's mirrors keep their status, got %q", got)
	}

	if n, err := models.MarkStaleSidecarConnectionsOffline(ctx, models.DB); err != nil || n != 0 {
		t.Fatalf("a fresh check-in keeps its mirrors online, changed %d, err %v", n, err)
	}

	execSQL(t, `UPDATE private.sidecars SET last_seen_at = NOW() - interval '179 seconds' WHERE id = ?`, pay.ID)
	if n, err := models.MarkStaleSidecarConnectionsOffline(ctx, models.DB); err != nil || n != 0 {
		t.Fatalf("a check-in inside the limit keeps its mirrors online, changed %d, err %v", n, err)
	}

	execSQL(t, `UPDATE private.sidecars SET last_seen_at = NOW() - interval '181 seconds' WHERE id = ?`, pay.ID)
	n, err := models.MarkStaleSidecarConnectionsOffline(ctx, models.DB)
	if err != nil || n != 2 {
		t.Fatalf("want both mirrors set offline, changed %d, err %v", n, err)
	}
	if got := mirrorStatus(t, pay.ID); got != models.ConnectionStatusOffline {
		t.Fatalf("a silent sidecar's mirrors are offline, got %q", got)
	}
	if got := queryString(t, `SELECT status::text FROM private.connections WHERE name = 'agent-pg'`); got != models.ConnectionStatusOnline {
		t.Fatalf("a connection with no sidecar keeps its status, got %q", got)
	}

	if err := models.RecordSidecarHandshake(models.DB, pay.ID, "1.0.0", "", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := models.MarkSidecarConnectionsOnline(models.DB, testOrgID, pay.ID); err != nil {
		t.Fatal(err)
	}
	if got := mirrorStatus(t, pay.ID); got != models.ConnectionStatusOnline {
		t.Fatalf("a sidecar that checks in again brings its mirrors back online, got %q", got)
	}
}
