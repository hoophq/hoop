package pgmanager

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// sqlPlan.IsEmpty
// ---------------------------------------------------------------------------

func TestSqlPlanIsEmpty(t *testing.T) {
	t.Run("zero value is empty", func(t *testing.T) {
		p := sqlPlan{}
		if !p.IsEmpty() {
			t.Error("expected zero-value plan to be empty")
		}
	})

	t.Run("CreateRole", func(t *testing.T) {
		p := sqlPlan{CreateRole: &createRoleSpec{Password: "pw"}}
		if p.IsEmpty() {
			t.Error("expected non-empty when CreateRole is set")
		}
	})

	t.Run("AlterAttributes", func(t *testing.T) {
		p := sqlPlan{AlterAttributes: []string{"LOGIN"}}
		if p.IsEmpty() {
			t.Error("expected non-empty when AlterAttributes set")
		}
	})

	t.Run("GrantMemberships", func(t *testing.T) {
		p := sqlPlan{GrantMemberships: []string{"parent_role"}}
		if p.IsEmpty() {
			t.Error("expected non-empty when GrantMemberships set")
		}
	})

	t.Run("RevokeMemberships", func(t *testing.T) {
		p := sqlPlan{RevokeMemberships: []string{"old_role"}}
		if p.IsEmpty() {
			t.Error("expected non-empty when RevokeMemberships set")
		}
	})

	t.Run("RotatePassword", func(t *testing.T) {
		p := sqlPlan{RotatePassword: &rotatePasswordSpec{Password: "new"}}
		if p.IsEmpty() {
			t.Error("expected non-empty when RotatePassword set")
		}
	})

	t.Run("GrantConnect", func(t *testing.T) {
		p := sqlPlan{Databases: []databasePlan{{Name: "db", GrantConnect: true}}}
		if p.IsEmpty() {
			t.Error("expected non-empty when GrantConnect is true")
		}
	})

	t.Run("BulkGrants", func(t *testing.T) {
		p := sqlPlan{Databases: []databasePlan{{
			Name:   "db",
			Scopes: []scopePlan{{Schema: "public", BulkGrants: []string{"SELECT"}}},
		}}}
		if p.IsEmpty() {
			t.Error("expected non-empty when BulkGrants set")
		}
	})

	t.Run("BulkRevokes", func(t *testing.T) {
		p := sqlPlan{Databases: []databasePlan{{
			Name:   "db",
			Scopes: []scopePlan{{Schema: "public", BulkRevokes: []string{"INSERT"}}},
		}}}
		if p.IsEmpty() {
			t.Error("expected non-empty when BulkRevokes set")
		}
	})

	t.Run("GrantUsage", func(t *testing.T) {
		p := sqlPlan{Databases: []databasePlan{{
			Name:   "db",
			Scopes: []scopePlan{{Schema: "public", GrantUsage: true}},
		}}}
		if p.IsEmpty() {
			t.Error("expected non-empty when GrantUsage is true")
		}
	})

	t.Run("database entry with no work inside is empty", func(t *testing.T) {
		p := sqlPlan{Databases: []databasePlan{{
			Name:         "db",
			GrantConnect: false,
			Scopes:       []scopePlan{{Schema: "public"}},
		}}}
		if !p.IsEmpty() {
			t.Error("expected empty when database has no actual work")
		}
	})
}

// ---------------------------------------------------------------------------
// buildSQLPlan
// ---------------------------------------------------------------------------

func TestBuildSQLPlan_NewRole(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", false)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if plan.CreateRole == nil {
		t.Fatal("expected CreateRole to be set for new role")
	}
	if len(plan.CreateRole.Password) == 0 {
		t.Error("expected non-empty password on CreateRole spec")
	}

	// LOGIN and INHERIT are true in defaultAttributes → appear in CreateRole.Attributes
	found := strSet(plan.CreateRole.Attributes)
	if _, ok := found["LOGIN"]; !ok {
		t.Errorf("expected LOGIN in CreateRole.Attributes, got %v", plan.CreateRole.Attributes)
	}
	if _, ok := found["INHERIT"]; !ok {
		t.Errorf("expected INHERIT in CreateRole.Attributes, got %v", plan.CreateRole.Attributes)
	}

	// AlterAttributes must be empty: CREATE ROLE already sets them.
	if len(plan.AlterAttributes) > 0 {
		t.Errorf("unexpected AlterAttributes when role is being created: %v", plan.AlterAttributes)
	}

	// Membership changes must be empty for new role (CREATE ROLE IN ROLE handles it).
	if len(plan.GrantMemberships) > 0 || len(plan.RevokeMemberships) > 0 {
		t.Errorf("unexpected membership changes for new role: grant=%v revoke=%v",
			plan.GrantMemberships, plan.RevokeMemberships)
	}

	if plan.IsEmpty() {
		t.Error("plan must not be empty when role needs to be created")
	}
}

func TestBuildSQLPlan_NewExternalRole_InRole(t *testing.T) {
	// External-mode: desired has a membership; for a new role, IN ROLE
	// should be set on CreateRole rather than GrantMemberships.
	desired := buildTestSnapshot("child_role", true)
	desired.Memberships = []string{"parent_role"}
	current := buildTestSnapshot("child_role", false)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if plan.CreateRole == nil {
		t.Fatal("expected CreateRole for non-existent role")
	}
	if len(plan.CreateRole.InRole) != 1 || plan.CreateRole.InRole[0] != "parent_role" {
		t.Errorf("expected InRole=[parent_role], got %v", plan.CreateRole.InRole)
	}
	// GrantMemberships must be empty — IN ROLE handles initial membership atomically.
	if len(plan.GrantMemberships) > 0 {
		t.Errorf("unexpected GrantMemberships for new role: %v", plan.GrantMemberships)
	}
}

func TestBuildSQLPlan_AlterAttributes_AddsMissing(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)
	current.Attributes["LOGIN"] = false // current is missing LOGIN

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if plan.CreateRole != nil {
		t.Error("unexpected CreateRole for existing role")
	}

	found := false
	for _, a := range plan.AlterAttributes {
		if a == "LOGIN" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected LOGIN in AlterAttributes, got %v", plan.AlterAttributes)
	}
}

func TestBuildSQLPlan_AlterAttributes_Additive(t *testing.T) {
	// An attribute true in current but false in desired must NOT be revoked.
	desired := buildTestSnapshot("myrole", true)
	desired.Attributes["CREATEDB"] = false
	current := buildTestSnapshot("myrole", true)
	current.Attributes["CREATEDB"] = true

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan.AlterAttributes {
		if a == "CREATEDB" {
			t.Error("CREATEDB must not appear in AlterAttributes (additive-only policy)")
		}
	}
}

func TestBuildSQLPlan_MembershipReconciliation(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.Memberships = []string{"new_parent"}
	current := buildTestSnapshot("myrole", true)
	current.Memberships = []string{"old_parent"}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.GrantMemberships) != 1 || plan.GrantMemberships[0] != "new_parent" {
		t.Errorf("expected GrantMemberships=[new_parent], got %v", plan.GrantMemberships)
	}
	if len(plan.RevokeMemberships) != 1 || plan.RevokeMemberships[0] != "old_parent" {
		t.Errorf("expected RevokeMemberships=[old_parent], got %v", plan.RevokeMemberships)
	}
}

func TestBuildSQLPlan_MembershipReconciliation_Sorted(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.Memberships = []string{"zzz", "aaa"}
	current := buildTestSnapshot("myrole", true)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.GrantMemberships) != 2 {
		t.Fatalf("expected 2 grant memberships, got %v", plan.GrantMemberships)
	}
	if plan.GrantMemberships[0] != "aaa" || plan.GrantMemberships[1] != "zzz" {
		t.Errorf("expected sorted GrantMemberships, got %v", plan.GrantMemberships)
	}
}

func TestBuildSQLPlan_PasswordRotation(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)

	plan, err := buildSQLPlan(true, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if plan.RotatePassword == nil {
		t.Fatal("expected RotatePassword to be set when rotateRequested=true")
	}
	if len(plan.RotatePassword.Password) == 0 {
		t.Error("expected non-empty rotation password")
	}
}

func TestBuildSQLPlan_NoRotationWhenNotRequested(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	current := buildTestSnapshot("myrole", true)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RotatePassword != nil {
		t.Error("expected RotatePassword=nil when rotateRequested=false")
	}
}

func TestBuildSQLPlan_InSync(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
	}
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
		TableCount:     3,
		Status:         "in-sync",
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.IsEmpty() {
		t.Error("expected empty plan for in-sync state")
	}
}

func TestBuildSQLPlan_BulkGrants(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"INSERT", "SELECT"},
	}
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
		TableCount:     3,
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) == 0 {
		t.Fatal("expected database plan entries")
	}
	db := plan.Databases[0]
	if db.Name != "mydb" {
		t.Errorf("expected database mydb, got %s", db.Name)
	}
	if len(db.Scopes) == 0 {
		t.Fatal("expected scope entries")
	}
	scope := db.Scopes[0]
	if len(scope.BulkGrants) != 1 || scope.BulkGrants[0] != "INSERT" {
		t.Errorf("expected BulkGrants=[INSERT], got %v", scope.BulkGrants)
	}
	if len(scope.BulkRevokes) != 0 {
		t.Errorf("expected no BulkRevokes, got %v", scope.BulkRevokes)
	}
}

func TestBuildSQLPlan_BulkRevokes(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
	}
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"INSERT", "SELECT"},
		TableCount:     3,
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) == 0 || len(plan.Databases[0].Scopes) == 0 {
		t.Fatal("expected database/scope plan entries")
	}
	scope := plan.Databases[0].Scopes[0]
	if len(scope.BulkRevokes) != 1 || scope.BulkRevokes[0] != "INSERT" {
		t.Errorf("expected BulkRevokes=[INSERT], got %v", scope.BulkRevokes)
	}
	if len(scope.BulkGrants) != 0 {
		t.Errorf("expected no BulkGrants, got %v", scope.BulkGrants)
	}
}

func TestBuildSQLPlan_GrantConnect(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
	}
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:    false,
		Usage:      false,
		TableCount: 3,
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) == 0 {
		t.Fatal("expected database plan entries")
	}
	if !plan.Databases[0].GrantConnect {
		t.Error("expected GrantConnect=true when current lacks CONNECT")
	}
	if len(plan.Databases[0].Scopes) == 0 {
		t.Fatal("expected scope entries")
	}
	if !plan.Databases[0].Scopes[0].GrantUsage {
		t.Error("expected GrantUsage=true when current lacks USAGE")
	}
}

func TestBuildSQLPlan_EmptySchemaSkipsBulkDiff(t *testing.T) {
	// When the live schema has zero tables the bulk diff is skipped — emitting
	// GRANT … ON ALL TABLES against zero tables would be a noisy no-op.
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
	}
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:    true,
		Usage:      true,
		TableCount: 0, // empty schema
		Status:     "empty-schema",
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}
	// CONNECT and USAGE already satisfied; no bulk diff for empty schema → empty plan.
	if !plan.IsEmpty() {
		t.Errorf("expected empty plan for empty schema (CONNECT/USAGE already met)")
	}
}

func TestBuildSQLPlan_NewScopeInDesired(t *testing.T) {
	// A scope present only in desired (role has no existing state for it)
	// should generate CONNECT, USAGE, and bulk grants.
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["newdb.analytics"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
	}
	current := buildTestSnapshot("myrole", true)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) == 0 {
		t.Fatal("expected database plan for new scope")
	}
	db := plan.Databases[0]
	if db.Name != "newdb" {
		t.Errorf("expected newdb, got %s", db.Name)
	}
	if !db.GrantConnect {
		t.Error("expected GrantConnect=true for new scope")
	}
	if len(db.Scopes) == 0 {
		t.Fatal("expected scope entries")
	}
	s := db.Scopes[0]
	if s.Schema != "analytics" {
		t.Errorf("expected schema analytics, got %s", s.Schema)
	}
	if !s.GrantUsage {
		t.Error("expected GrantUsage=true for new scope")
	}
	if len(s.BulkGrants) == 0 {
		t.Error("expected BulkGrants for new scope")
	}
}

func TestBuildSQLPlan_DatabasesAlphabeticallyOrdered(t *testing.T) {
	desired := buildTestSnapshot("myrole", true)
	desired.ScopeStates["zdb.public"] = Scope{Connect: true, Usage: true, BulkPrivileges: []string{"SELECT"}}
	desired.ScopeStates["adb.public"] = Scope{Connect: true, Usage: true, BulkPrivileges: []string{"SELECT"}}
	current := buildTestSnapshot("myrole", true)

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) != 2 {
		t.Fatalf("expected 2 databases, got %d", len(plan.Databases))
	}
	if plan.Databases[0].Name != "adb" || plan.Databases[1].Name != "zdb" {
		t.Errorf("expected [adb, zdb] (alphabetical), got [%s, %s]",
			plan.Databases[0].Name, plan.Databases[1].Name)
	}
}

func TestBuildSQLPlan_ScopeOnlyInCurrent_Revokes(t *testing.T) {
	// A scope present only in current (not in desired) should trigger bulk revokes
	// for all privileges that role holds on it.
	desired := buildTestSnapshot("myrole", true)
	// desired has NO ScopeStates
	current := buildTestSnapshot("myrole", true)
	current.ScopeStates["mydb.public"] = Scope{
		Connect:        true,
		Usage:          true,
		BulkPrivileges: []string{"SELECT"},
		TableCount:     2,
	}

	plan, err := buildSQLPlan(false, desired, current)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Databases) == 0 || len(plan.Databases[0].Scopes) == 0 {
		t.Fatal("expected scope plan to revoke current privileges")
	}
	scope := plan.Databases[0].Scopes[0]
	if len(scope.BulkRevokes) == 0 {
		t.Error("expected BulkRevokes for scope not in desired")
	}
	found := false
	for _, r := range scope.BulkRevokes {
		if r == "SELECT" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SELECT in BulkRevokes, got %v", scope.BulkRevokes)
	}
}

// ---------------------------------------------------------------------------
// sqlPlan.Render
// ---------------------------------------------------------------------------

func TestRender_EmptyPlan(t *testing.T) {
	p := sqlPlan{Role: "myrole"}
	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "-- no changes") {
		t.Errorf("expected '-- no changes' for empty plan, got:\n%s", sql)
	}
}

func TestRender_CreateRole(t *testing.T) {
	p := sqlPlan{
		Role: "myrole",
		CreateRole: &createRoleSpec{
			Password:   "secretpw",
			Attributes: []string{"INHERIT", "LOGIN"},
		},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `CREATE ROLE "myrole"`) {
		t.Errorf("expected CREATE ROLE in output:\n%s", sql)
	}
	// Template always emits the placeholder, never the spec's Password field.
	if !strings.Contains(sql, "ROLE_PASSWORD_PLACEHOLDER") {
		t.Errorf("expected ROLE_PASSWORD_PLACEHOLDER in output:\n%s", sql)
	}
	if strings.Contains(sql, "secretpw") {
		t.Error("literal password must not appear in rendered SQL")
	}
}

func TestRender_CreateRoleWithInRole(t *testing.T) {
	p := sqlPlan{
		Role: "child",
		CreateRole: &createRoleSpec{
			Password:   "pw",
			Attributes: []string{"LOGIN"},
			InRole:     []string{"parent_role"},
		},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `IN ROLE`) {
		t.Errorf("expected IN ROLE clause in CREATE ROLE output:\n%s", sql)
	}
	if !strings.Contains(sql, `"parent_role"`) {
		t.Errorf("expected quoted parent_role in output:\n%s", sql)
	}
}

func TestRender_AlterAttributes(t *testing.T) {
	p := sqlPlan{
		Role:            "myrole",
		AlterAttributes: []string{"INHERIT", "LOGIN"},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `ALTER ROLE "myrole"`) {
		t.Errorf("expected ALTER ROLE in output:\n%s", sql)
	}
}

func TestRender_MembershipChanges(t *testing.T) {
	p := sqlPlan{
		Role:              "myrole",
		GrantMemberships:  []string{"parent_role"},
		RevokeMemberships: []string{"old_role"},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `GRANT "parent_role" TO "myrole"`) {
		t.Errorf("expected GRANT membership in output:\n%s", sql)
	}
	if !strings.Contains(sql, `REVOKE "old_role" FROM "myrole"`) {
		t.Errorf("expected REVOKE membership in output:\n%s", sql)
	}
}

func TestRender_RotatePassword(t *testing.T) {
	p := sqlPlan{
		Role:           "myrole",
		RotatePassword: &rotatePasswordSpec{Password: "newsecret"},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `ALTER ROLE "myrole" WITH PASSWORD 'ROLE_PASSWORD_PLACEHOLDER'`) {
		t.Errorf("expected password rotation ALTER ROLE in output:\n%s", sql)
	}
	if strings.Contains(sql, "newsecret") {
		t.Error("literal rotation password must not appear in rendered SQL")
	}
}

func TestRender_PerScopeGrants(t *testing.T) {
	p := sqlPlan{
		Role: "myrole",
		Databases: []databasePlan{{
			Name:         "mydb",
			GrantConnect: true,
			Scopes: []scopePlan{{
				Schema:     "public",
				GrantUsage: true,
				BulkGrants: []string{"INSERT", "SELECT"},
			}},
		}},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `GRANT CONNECT ON DATABASE "mydb" TO "myrole"`) {
		t.Errorf("expected GRANT CONNECT:\n%s", sql)
	}
	if !strings.Contains(sql, `GRANT USAGE ON SCHEMA "public" TO "myrole"`) {
		t.Errorf("expected GRANT USAGE:\n%s", sql)
	}
	if !strings.Contains(sql, `GRANT INSERT, SELECT ON ALL TABLES IN SCHEMA "public" TO "myrole"`) {
		t.Errorf("expected GRANT ALL TABLES:\n%s", sql)
	}
	if !strings.Contains(sql, "BEGIN;") || !strings.Contains(sql, "COMMIT;") {
		t.Errorf("expected BEGIN/COMMIT transaction:\n%s", sql)
	}
}

func TestRender_BulkRevokes(t *testing.T) {
	p := sqlPlan{
		Role: "myrole",
		Databases: []databasePlan{{
			Name: "mydb",
			Scopes: []scopePlan{{
				Schema:      "public",
				BulkRevokes: []string{"DELETE", "INSERT"},
			}},
		}},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `REVOKE DELETE, INSERT ON ALL TABLES IN SCHEMA "public" FROM "myrole"`) {
		t.Errorf("expected REVOKE ALL TABLES:\n%s", sql)
	}
}

func TestRender_IdentifierQuoting(t *testing.T) {
	// Identifiers containing double quotes must be properly escaped.
	p := sqlPlan{
		Role: `role"with"quotes`,
		CreateRole: &createRoleSpec{
			Password:   "pw",
			Attributes: []string{"LOGIN"},
		},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `"role""with""quotes"`) {
		t.Errorf("expected doubled double-quotes in identifier:\n%s", sql)
	}
}

func TestRender_ConnectDirective(t *testing.T) {
	p := sqlPlan{
		Role: "myrole",
		Databases: []databasePlan{{
			Name:         "mydb",
			GrantConnect: true,
			Scopes:       []scopePlan{},
		}},
	}

	sql, err := p.Render()
	if err != nil {
		t.Fatal(err)
	}
	// The template emits \connect <dbname> before the transaction block.
	if !strings.Contains(sql, `\connect "mydb"`) {
		t.Errorf("expected \\connect directive:\n%s", sql)
	}
}
