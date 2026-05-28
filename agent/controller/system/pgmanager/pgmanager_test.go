package pgmanager

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// expandManaged
// ---------------------------------------------------------------------------

func TestExpandManaged_BasicScopes(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Type:       typeManaged,
		Scopes:     []string{"shop.public", "shop.analytics"},
		Privileges: []string{"SELECT", "INSERT"},
	}

	desired := expandManaged(config)

	if desired.Role != "myrole" {
		t.Errorf("expected role=myrole, got %s", desired.Role)
	}
	if !desired.Exists {
		t.Error("desired state must assert Exists=true")
	}
	if len(desired.Memberships) != 0 {
		t.Errorf("expected empty memberships for managed mode, got %v", desired.Memberships)
	}

	for _, key := range []string{"shop.public", "shop.analytics"} {
		scope, ok := desired.ScopeStates[key]
		if !ok {
			t.Errorf("expected scope %s in desired state", key)
			continue
		}
		if !scope.Connect {
			t.Errorf("expected Connect=true for scope %s", key)
		}
		if !scope.Usage {
			t.Errorf("expected Usage=true for scope %s", key)
		}
		if len(scope.BulkPrivileges) != 2 {
			t.Errorf("expected 2 bulk privileges for scope %s, got %v", key, scope.BulkPrivileges)
		}
	}
}

func TestExpandManaged_PrivilegesUppercasedAndSorted(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"db.schema"},
		Privileges: []string{"select", "insert", "DELETE"},
	}
	desired := expandManaged(config)

	scope := desired.ScopeStates["db.schema"]
	if len(scope.BulkPrivileges) != 3 {
		t.Fatalf("expected 3 privileges, got %v", scope.BulkPrivileges)
	}
	// Sorted uppercase: DELETE, INSERT, SELECT
	if scope.BulkPrivileges[0] != "DELETE" || scope.BulkPrivileges[1] != "INSERT" || scope.BulkPrivileges[2] != "SELECT" {
		t.Errorf("expected [DELETE, INSERT, SELECT], got %v", scope.BulkPrivileges)
	}
}

func TestExpandManaged_BareDBDefaultsToPublic(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"mydb"},
		Privileges: []string{"SELECT"},
	}
	desired := expandManaged(config)

	if _, ok := desired.ScopeStates["mydb.public"]; !ok {
		t.Error("expected mydb.public scope for bare 'mydb' scope path")
	}
}

func TestExpandManaged_DefaultAttributes(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"db"},
		Privileges: []string{},
	}
	desired := expandManaged(config)

	if !desired.Attributes["LOGIN"] {
		t.Error("expected LOGIN=true in desired attributes")
	}
	if !desired.Attributes["INHERIT"] {
		t.Error("expected INHERIT=true in desired attributes")
	}
	if desired.Attributes["SUPERUSER"] {
		t.Error("expected SUPERUSER=false in desired attributes")
	}
}

func TestExpandManaged_EmptyPrivileges(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"mydb.myscope"},
		Privileges: []string{},
	}
	desired := expandManaged(config)
	scope := desired.ScopeStates["mydb.myscope"]
	if len(scope.BulkPrivileges) != 0 {
		t.Errorf("expected empty BulkPrivileges, got %v", scope.BulkPrivileges)
	}
}

func TestExpandManaged_WhitespacePrivilegeSkipped(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"mydb.myscope"},
		Privileges: []string{"SELECT", "   ", ""},
	}
	desired := expandManaged(config)
	scope := desired.ScopeStates["mydb.myscope"]
	if len(scope.BulkPrivileges) != 1 || scope.BulkPrivileges[0] != "SELECT" {
		t.Errorf("expected only [SELECT] after filtering blanks, got %v", scope.BulkPrivileges)
	}
}

func TestExpandManaged_ScopesSharedPrivilegeList(t *testing.T) {
	// All scopes get the same privilege slice (same instance is fine because
	// buildSQLPlan never mutates the slice).
	config := &Config{
		RoleName:   "myrole",
		Scopes:     []string{"db.s1", "db.s2"},
		Privileges: []string{"SELECT"},
	}
	desired := expandManaged(config)

	for _, key := range []string{"db.s1", "db.s2"} {
		scope, ok := desired.ScopeStates[key]
		if !ok {
			t.Errorf("missing scope %s", key)
			continue
		}
		if len(scope.BulkPrivileges) != 1 || scope.BulkPrivileges[0] != "SELECT" {
			t.Errorf("unexpected privileges for scope %s: %v", key, scope.BulkPrivileges)
		}
	}
}

// ---------------------------------------------------------------------------
// buildPlan
// ---------------------------------------------------------------------------

func TestBuildPlan_InSync(t *testing.T) {
	config := &Config{SID: "test-sid", RoleName: "myrole", Type: typeManaged}
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)

	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Status != "in-sync" {
		t.Errorf("expected status=in-sync, got %s", resp.Status)
	}
	if !resp.RoleExists {
		t.Error("expected RoleExists=true")
	}
	if resp.SQLPlanChecksum == "" {
		t.Error("expected non-empty SQLPlanChecksum")
	}
	if len(resp.StateMigration) == 0 {
		t.Error("expected non-empty StateMigration YAML")
	}
}

func TestBuildPlan_OutOfSync(t *testing.T) {
	config := &Config{SID: "test-sid", RoleName: "myrole", Type: typeManaged}
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", false) // role doesn't exist yet

	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Status != "out-of-sync" {
		t.Errorf("expected status=out-of-sync for new role, got %s", resp.Status)
	}
	if resp.RoleExists {
		t.Error("expected RoleExists=false for new role")
	}
}

func TestBuildPlan_ChecksumMatchesStateMigration(t *testing.T) {
	// The checksum returned in planResponse must equal the one embedded
	// in the StateMigration YAML — they must be generated together.
	config := &Config{SID: "sid1", RoleName: "myrole"}
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)

	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}

	var migration StateMigration
	if err := yaml.Unmarshal(resp.StateMigration, &migration); err != nil {
		t.Fatalf("failed to unmarshal StateMigration: %v", err)
	}
	if migration.SQLPlanChecksum != resp.SQLPlanChecksum {
		t.Errorf("checksum mismatch: planResponse=%s StateMigration=%s",
			resp.SQLPlanChecksum, migration.SQLPlanChecksum)
	}
}

func TestBuildPlan_StateMigrationContent(t *testing.T) {
	config := &Config{
		SID:      "test-sid-123",
		RoleName: "myrole",
		Type:     typeManaged,
		Scopes:   []string{"mydb.public"},
	}
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)

	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}

	var migration StateMigration
	if err := yaml.Unmarshal(resp.StateMigration, &migration); err != nil {
		t.Fatalf("failed to unmarshal StateMigration: %v", err)
	}

	if migration.SID != "test-sid-123" {
		t.Errorf("expected SID=test-sid-123, got %s", migration.SID)
	}
	if migration.Config == nil {
		t.Fatal("expected Config to be set in StateMigration")
	}
	if migration.Config.RoleName != "myrole" {
		t.Errorf("expected RoleName=myrole in Config, got %s", migration.Config.RoleName)
	}
	if migration.CurrentState == nil {
		t.Error("expected CurrentState to be set in StateMigration")
	}
}

func TestBuildPlan_RequiresMigrationFlag(t *testing.T) {
	config := &Config{SID: "sid", RoleName: "myrole"}

	// In-sync: RequiresMigration must be false.
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)
	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}
	var m StateMigration
	if err := yaml.Unmarshal(resp.StateMigration, &m); err != nil {
		t.Fatal(err)
	}
	if m.CurrentState.RequiresMigration {
		t.Error("expected RequiresMigration=false for in-sync state")
	}

	// Out-of-sync: RequiresMigration must be true.
	current2 := buildTestSnapshot("myrole", false)
	resp2, err := buildPlan(config, current2, desired)
	if err != nil {
		t.Fatal(err)
	}
	var m2 StateMigration
	if err := yaml.Unmarshal(resp2.StateMigration, &m2); err != nil {
		t.Fatal(err)
	}
	if !m2.CurrentState.RequiresMigration {
		t.Error("expected RequiresMigration=true for out-of-sync state")
	}
}

func TestBuildPlan_SQLPlanPresentInMigration(t *testing.T) {
	config := &Config{SID: "sid", RoleName: "myrole"}
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", false) // triggers CREATE ROLE

	resp, err := buildPlan(config, current, desired)
	if err != nil {
		t.Fatal(err)
	}

	var migration StateMigration
	if err := yaml.Unmarshal(resp.StateMigration, &migration); err != nil {
		t.Fatalf("failed to unmarshal StateMigration: %v", err)
	}
	if migration.SQLPlan.Value == "" {
		t.Error("expected SQLPlan.Value to be non-empty in StateMigration")
	}
}

// ---------------------------------------------------------------------------
// planManaged validation (pre-psql checks only)
// ---------------------------------------------------------------------------

func TestPlanManaged_SourceRoleNotAllowed(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		SourceRole: "parent", // not valid for managed mode
		Scopes:     []string{"mydb.public"},
	}
	_, err := planManaged(config, ConnectionParts{Host: "localhost"})
	if err == nil {
		t.Error("expected error when source_role is set for managed mode")
	}
}

func TestPlanManaged_ScopesRequired(t *testing.T) {
	config := &Config{
		RoleName: "myrole",
		Scopes:   []string{},
	}
	_, err := planManaged(config, ConnectionParts{Host: "localhost"})
	if err == nil {
		t.Error("expected error when scopes is empty for managed mode")
	}
}

// ---------------------------------------------------------------------------
// planExternal validation (pre-psql checks only)
// ---------------------------------------------------------------------------

func TestPlanExternal_SourceRoleRequired(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		SourceRole: "", // required for external mode
	}
	_, err := planExternal(config, ConnectionParts{Host: "localhost"})
	if err == nil {
		t.Error("expected error when source_role is empty for external mode")
	}
}

func TestPlanExternal_ScopesNotAllowed(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		SourceRole: "parent",
		Scopes:     []string{"mydb.public"}, // not allowed for external mode
	}
	_, err := planExternal(config, ConnectionParts{Host: "localhost"})
	if err == nil {
		t.Error("expected error when scopes set for external mode")
	}
}

func TestPlanExternal_PrivilegesNotAllowed(t *testing.T) {
	config := &Config{
		RoleName:   "myrole",
		SourceRole: "parent",
		Privileges: []string{"SELECT"}, // not allowed for external mode
	}
	_, err := planExternal(config, ConnectionParts{Host: "localhost"})
	if err == nil {
		t.Error("expected error when privileges set for external mode")
	}
}
