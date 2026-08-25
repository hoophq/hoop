package models_test

import (
	"slices"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

// seedPluginConnection wires a connection to a plugin with the given group
// config, creating both if they do not exist yet.
func seedPluginConnection(t *testing.T, pluginName, connName string, enabled bool, config []string) {
	t.Helper()
	execSQL(t, `
		INSERT INTO private.plugins (org_id, name) VALUES (?, ?)
		ON CONFLICT (org_id, name) DO NOTHING`, testOrgID, pluginName)
	seedConnection(t, connName, "enabled")
	execSQL(t, `
		INSERT INTO private.plugin_connections (org_id, plugin_id, connection_id, enabled, config)
		SELECT ?, p.id, c.id, ?, ?
		FROM private.plugins p, private.connections c
		WHERE p.org_id = ? AND p.name = ? AND c.org_id = ? AND c.name = ?`,
		testOrgID, enabled, pq.StringArray(config), testOrgID, pluginName, testOrgID, connName)
}

func TestListAccessControlGroupNames(t *testing.T) {
	startTestDB(t)

	seedPluginConnection(t, "access_control", "ac-conn-one", true,
		[]string{"CG-Hoop-BANKING-PF", "CG-Hoop-BANKING-PJ"})
	// Same group on a second connection: the result is deduped.
	seedPluginConnection(t, "access_control", "ac-conn-two", true,
		[]string{"CG-Hoop-BANKING-PF", "sre"})
	// A disabled association is invisible to GetPluginByName, so it must be
	// invisible here too, or the two disagree.
	seedPluginConnection(t, "access_control", "ac-conn-disabled", false,
		[]string{"CG-Hoop-DISABLED"})
	// Another plugin's config is not a group list.
	seedPluginConnection(t, "audit", "ac-conn-audit", true, []string{"not-a-group"})
	// A row the Access Control page never wrote carries a null config.
	seedPluginConnection(t, "access_control", "ac-conn-null", true, nil)

	names, err := models.ListAccessControlGroupNames(models.DB, testOrgID)
	if err != nil {
		t.Fatalf("list access control group names: %v", err)
	}
	slices.Sort(names)

	want := []string{"CG-Hoop-BANKING-PF", "CG-Hoop-BANKING-PJ", "sre"}
	if !slices.Equal(names, want) {
		t.Errorf("group names: want %v, got %v", want, names)
	}
}

// An org with no access_control plugin at all is the common case and must not
// error: it simply contributes nothing to the group listing.
func TestListAccessControlGroupNamesWithoutPlugin(t *testing.T) {
	startTestDB(t)

	names, err := models.ListAccessControlGroupNames(models.DB, testOrgID)
	if err != nil {
		t.Fatalf("list access control group names: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("group names: want empty, got %v", names)
	}
}
