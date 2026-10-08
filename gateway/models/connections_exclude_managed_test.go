package models_test

import (
	"sort"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func connectionNames(items []models.Connection) []string {
	var out []string
	for _, c := range items {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

func resourceNames(items []models.Resources) []string {
	var out []string
	for _, r := range items {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

func equalNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: want %v, got %v", what, want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: want %v, got %v", what, want, got)
		}
	}
}

// The list endpoints set ExcludeManagedBy so they show no sidecar mirror.
// Without the option, the model lists answer as before.
func TestExcludeManagedByLeavesOutSidecarMirrors(t *testing.T) {
	startTestDB(t)
	ctx := models.NewAdminContext(testOrgID)
	sc := seedSidecar(t, "pay")
	if err := syncMirrors(t, sc, daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "api", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	seedAdminConnection(t, "agent-pg", "agent-pg")
	// An admin put a connection of their own on the mirror's resource: the
	// resource is theirs too, so it stays listed.
	execSQL(t, `INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'pay-api-ro', 'custom', 'redis', 'pay-api')`, testOrgID)

	all := models.ConnectionFilterOption{}
	without := models.ConnectionFilterOption{ExcludeManagedBy: models.ConnectionManagedBySidecar}

	listed, err := models.ListConnections(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	equalNames(t, "connections, no option", connectionNames(listed), []string{"agent-pg", "pay-api", "pay-api-ro", "pay-appdb"})

	listed, err = models.ListConnections(ctx, without)
	if err != nil {
		t.Fatal(err)
	}
	equalNames(t, "connections, excluded", connectionNames(listed), []string{"agent-pg", "pay-api-ro"})

	page, total, err := models.ListConnectionsPaginated(testOrgID, []string{"admin"},
		models.ConnectionPaginationOption{ConnectionFilterOption: without, Page: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 1 || page[0].Name != "agent-pg" {
		t.Fatalf("paginated, excluded: want agent-pg of 2, got %v of %d", connectionNames(page), total)
	}

	resources, total, err := models.ListResources(models.DB, testOrgID, []string{"admin"}, true, models.ResourceFilterOption{})
	if err != nil {
		t.Fatal(err)
	}
	equalNames(t, "resources, no option", resourceNames(resources), []string{"agent-pg", "pay-api", "pay-appdb"})
	if total != 3 {
		t.Fatalf("resources, no option: want total 3, got %d", total)
	}

	resources, total, err = models.ListResources(models.DB, testOrgID, []string{"admin"}, true,
		models.ResourceFilterOption{ExcludeManagedBy: models.ConnectionManagedBySidecar})
	if err != nil {
		t.Fatal(err)
	}
	equalNames(t, "resources, excluded", resourceNames(resources), []string{"agent-pg", "pay-api"})
	if total != 2 {
		t.Fatalf("resources, excluded: want total 2, got %d", total)
	}

	// With access control on, a user in "devs" may see agent-pg and the
	// mirror pay-api, not pay-api-ro. For that user pay-api holds only a
	// mirror, so it is left out too.
	execSQL(t, `INSERT INTO private.plugins (org_id, name) VALUES (?, 'access_control')`, testOrgID)
	execSQL(t, `INSERT INTO private.plugin_connections (org_id, plugin_id, connection_id, config)
		SELECT c.org_id, p.id, c.id, '{devs}' FROM private.connections c
		JOIN private.plugins p ON p.org_id = c.org_id AND p.name = 'access_control'
		WHERE c.org_id = ? AND c.name IN ('agent-pg', 'pay-api')`, testOrgID)
	resources, _, err = models.ListResources(models.DB, testOrgID, []string{"devs"}, false,
		models.ResourceFilterOption{ExcludeManagedBy: models.ConnectionManagedBySidecar})
	if err != nil {
		t.Fatal(err)
	}
	equalNames(t, "resources, excluded, devs", resourceNames(resources), []string{"agent-pg"})
}
