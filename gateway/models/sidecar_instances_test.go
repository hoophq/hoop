package models_test

import (
	"context"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

func instanceNames(t *testing.T, items []models.SidecarInstance) []string {
	t.Helper()
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	return names
}

// Two pods behind one sidecar keep their own rows; a pod's next handshake
// updates its row rather than adding one.
func TestSidecarInstancesAreRecordedApart(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "edge")

	record := func(name, version, outcome string) {
		t.Helper()
		if err := models.RecordSidecarInstanceHandshake(models.DB, testOrgID, sc.ID, name, version, "rev-1", outcome, ""); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	record("pod-b", "1.0.0", "applied")
	record("pod-a", "1.0.0", "applied")
	execSQL(t, `UPDATE private.sidecar_instances SET last_seen_at = NOW() - interval '1 hour' WHERE name = 'pod-a'`)
	record("pod-b", "1.1.0", "restart")

	items, err := models.ListSidecarInstances(models.DB, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := instanceNames(t, items); len(got) != 2 || got[0] != "pod-a" || got[1] != "pod-b" {
		t.Fatalf("instances = %v, want pod-a, pod-b", got)
	}
	a, b := items[0], items[1]
	if *a.ReportedVersion != "1.0.0" || *b.ReportedVersion != "1.1.0" {
		t.Fatalf("versions = %q, %q", *a.ReportedVersion, *b.ReportedVersion)
	}
	if *b.LastOutcome != "restart" {
		t.Fatalf("pod-b outcome = %q, want the last report", *b.LastOutcome)
	}
	if !a.LastSeenAt.Before(b.LastSeenAt.Add(-30 * time.Minute)) {
		t.Fatalf("pod-a last_seen_at %v was refreshed by pod-b's handshake at %v", a.LastSeenAt, b.LastSeenAt)
	}
	if b.FirstSeenAt.After(b.LastSeenAt) {
		t.Fatalf("pod-b first_seen_at %v after last_seen_at %v", b.FirstSeenAt, b.LastSeenAt)
	}

	other := seedSidecar(t, "other")
	if items, err := models.ListSidecarInstances(models.DB, other.ID); err != nil || len(items) != 0 {
		t.Fatalf("another sidecar lists no instances, got %v, err %v", items, err)
	}
}

// A pruning pass removes only the rows silent past the cutoff, and deleting
// the sidecar removes the rest.
func TestStaleSidecarInstancesArePruned(t *testing.T) {
	startTestDB(t)
	ctx := context.Background()
	sc := seedSidecar(t, "prune")
	for _, name := range []string{"fresh", "stale"} {
		if err := models.RecordSidecarInstanceHandshake(models.DB, testOrgID, sc.ID, name, "1.0.0", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, `UPDATE private.sidecar_instances SET last_seen_at = NOW() - interval '25 hours' WHERE name = 'stale'`)

	n, err := models.DeleteStaleSidecarInstances(ctx, models.DB, models.SidecarInstanceRetention)
	if err != nil || n != 1 {
		t.Fatalf("want one stale row removed, got %d, err %v", n, err)
	}
	items, err := models.ListSidecarInstances(models.DB, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := instanceNames(t, items); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("instances after the prune = %v, want fresh", got)
	}

	if _, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, sc.ID); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, `SELECT count(*)::text FROM private.sidecar_instances WHERE sidecar_id = ?`, sc.ID); got != "0" {
		t.Fatalf("deleting the sidecar left %s instance row(s)", got)
	}
}
