//go:build smoke

package services

// Manual smoke test for the protection profile lifecycle. Requires a scratch
// PostgreSQL database (it runs all migrations and writes data):
//
//	docker exec postgres psql -U postgres -c 'DROP DATABASE IF EXISTS pp_smoke' -c 'CREATE DATABASE pp_smoke'
//	PP_SMOKE_DB='postgres://postgres:password@127.0.0.1:5432/pp_smoke?sslmode=disable' \
//	  go test -tags smoke -run TestProtectionProfileLifecycleSmoke ./services/ -v -count=1

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
)

func TestProtectionProfileLifecycleSmoke(t *testing.T) {
	dsn := os.Getenv("PP_SMOKE_DB")
	if dsn == "" {
		t.Skip("PP_SMOKE_DB not set")
	}
	if err := modelsbootstrap.MigrateDB(dsn, ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	if err := models.InitDatabaseConnection(dsn, 5); err != nil {
		t.Fatalf("db connect failed: %v", err)
	}

	orgID := uuid.New()
	mustExec(t, `INSERT INTO private.orgs (id, name) VALUES (?, ?)`, orgID, "pp-smoke-org")
	mustExec(t, `INSERT INTO private.agents (org_id, id, name, mode, key_hash, status)
		VALUES (?, ?, 'smoke-agent', 'standard', 'x', 'DISCONNECTED')`, orgID, uuid.New())
	agentID := scanStr(t, `SELECT id FROM private.agents WHERE org_id = ?`, orgID)
	for _, name := range []string{"conn-a", "conn-b"} {
		mustExec(t, `INSERT INTO private.resources (org_id, name, subtype, type)
			VALUES (?, ?, 'postgres', 'database')`, orgID, name)
		mustExec(t, `INSERT INTO private.connections (id, org_id, agent_id, name, resource_name, command, type, status)
			VALUES (?, ?, ?, ?, ?, '{}', 'database', 'offline')`, uuid.New(), orgID, agentID, name, name)
	}

	ctx := context.Background()
	medium, high := ProtectionProfileProtectionMedium, ProtectionProfileProtectionHigh

	// 1. First selection: Balanced. Everything materialized, both connections tagged.
	res, err := ApplyOrgProtectionProfile(ctx, orgID, &medium, "")
	if err != nil {
		t.Fatalf("apply medium: %v", err)
	}
	if res.PreviousProfile != nil || res.ConnectionsAffected != 2 {
		t.Fatalf("apply medium: prev=%v affected=%d, want nil/2", res.PreviousProfile, res.ConnectionsAffected)
	}
	assertCount(t, 1, `SELECT COUNT(*) FROM private.attributes WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 7, `SELECT COUNT(*) FROM private.guardrail_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 1, `SELECT COUNT(*) FROM private.datamasking_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 2, `SELECT COUNT(*) FROM private.access_request_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 1, `SELECT COUNT(*) FROM private.ai_session_analyzer_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 2, `SELECT COUNT(*) FROM private.connections_attributes WHERE org_id = ? AND attribute_name = ?`,
		orgID, protectionProfileCatalog[medium].AttributeName)
	if got := scanStr(t, `SELECT default_protection_profile FROM private.orgs WHERE id = ?`, orgID); got != medium {
		t.Fatalf("org profile = %q, want %q", got, medium)
	}

	// 2. Re-apply same profile: idempotent no-op.
	if _, err := ApplyOrgProtectionProfile(ctx, orgID, &medium, ""); err != nil {
		t.Fatalf("re-apply medium: %v", err)
	}
	assertCount(t, 7, `SELECT COUNT(*) FROM private.guardrail_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)

	// 3. Switch to Maximum: shared rules survive, medium-only rows GC'd,
	//    connections move to the new attribute.
	res, err = ApplyOrgProtectionProfile(ctx, orgID, &high, "")
	if err != nil {
		t.Fatalf("apply high: %v", err)
	}
	if res.PreviousProfile == nil || *res.PreviousProfile != medium || res.ConnectionsAffected != 2 {
		t.Fatalf("apply high: prev=%v affected=%d, want medium/2", res.PreviousProfile, res.ConnectionsAffected)
	}
	assertCount(t, 1, `SELECT COUNT(*) FROM private.attributes WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 12, `SELECT COUNT(*) FROM private.guardrail_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	// "Hoop - Confidential data" (medium-only) GC'd, "Hoop - Full masking" created.
	assertCount(t, 1, `SELECT COUNT(*) FROM private.datamasking_rules WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	assertCount(t, 0, `SELECT COUNT(*) FROM private.datamasking_rules WHERE org_id = ? AND name = 'Hoop - Confidential data'`, orgID)
	assertCount(t, 0, `SELECT COUNT(*) FROM private.connections_attributes WHERE org_id = ? AND attribute_name = ?`,
		orgID, protectionProfileCatalog[medium].AttributeName)
	assertCount(t, 2, `SELECT COUNT(*) FROM private.connections_attributes WHERE org_id = ? AND attribute_name = ?`,
		orgID, protectionProfileCatalog[high].AttributeName)

	// 4. Analyzer resolution through the attribute junction.
	rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, "conn-a")
	if err != nil {
		t.Fatalf("analyzer resolution: %v", err)
	}
	if rule.Name != "Hoop - Block high risk" {
		t.Fatalf("analyzer rule = %q, want %q", rule.Name, "Hoop - Block high risk")
	}

	// 4b. Approval settings customized on a managed access rule survive a
	//     profile re-apply (materialize never rewrites existing rows).
	mustExec(t, `UPDATE private.access_request_rules
		SET reviewers_groups = '{sre,admin}', min_approvals = 2
		WHERE org_id = ? AND name = 'Hoop_Command_approval'`, orgID)
	if _, err := ApplyOrgProtectionProfile(ctx, orgID, &high, ""); err != nil {
		t.Fatalf("re-apply high after customization: %v", err)
	}
	if got := scanStr(t, `SELECT min_approvals::text FROM private.access_request_rules
		WHERE org_id = ? AND name = 'Hoop_Command_approval'`, orgID); got != "2" {
		t.Fatalf("customized min_approvals = %s, want 2 (re-apply must not rewrite managed rules)", got)
	}

	// 5. New connection inherits the active profile attribute.
	mustExec(t, `INSERT INTO private.resources (org_id, name, subtype, type)
		VALUES (?, 'conn-c', 'postgres', 'database')`, orgID)
	mustExec(t, `INSERT INTO private.connections (id, org_id, agent_id, name, resource_name, command, type, status)
		VALUES (?, ?, ?, 'conn-c', 'conn-c', '{}', 'database', 'offline')`, uuid.New(), orgID, agentID)
	if err := TagConnectionWithActiveProfile(ctx, orgID.String(), "conn-c"); err != nil {
		t.Fatalf("auto-tag: %v", err)
	}
	assertCount(t, 3, `SELECT COUNT(*) FROM private.connections_attributes WHERE org_id = ? AND attribute_name = ?`,
		orgID, protectionProfileCatalog[high].AttributeName)

	// 6. Manual configuration: everything managed disappears, org column NULL.
	res, err = ApplyOrgProtectionProfile(ctx, orgID, nil, "")
	if err != nil {
		t.Fatalf("apply manual: %v", err)
	}
	if res.PreviousProfile == nil || *res.PreviousProfile != high || res.ConnectionsAffected != 3 {
		t.Fatalf("apply manual: prev=%v affected=%d, want high/3", res.PreviousProfile, res.ConnectionsAffected)
	}
	for _, table := range []string{"attributes", "guardrail_rules", "datamasking_rules", "access_request_rules", "ai_session_analyzer_rules"} {
		assertCount(t, 0, `SELECT COUNT(*) FROM private.`+table+` WHERE org_id = ? AND managed_by = 'hoop'`, orgID)
	}
	var profile *string
	if err := models.DB.Raw(`SELECT default_protection_profile FROM private.orgs WHERE id = ?`, orgID).Scan(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if profile != nil {
		t.Fatalf("org profile = %v, want NULL", *profile)
	}

	// 7. Invalid profile rejected.
	bogus := "not-a-profile"
	if _, err := ApplyOrgProtectionProfile(ctx, orgID, &bogus, ""); err != ErrInvalidProtectionProfile {
		t.Fatalf("bogus profile err = %v, want ErrInvalidProtectionProfile", err)
	}
}

func mustExec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := models.DB.Exec(q, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func scanStr(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	if err := models.DB.Raw(q, args...).Scan(&s).Error; err != nil {
		t.Fatalf("scan %q: %v", q, err)
	}
	return s
}

func assertCount(t *testing.T, want int64, q string, args ...any) {
	t.Helper()
	var got int64
	if err := models.DB.Raw(q, args...).Scan(&got).Error; err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	if got != want {
		t.Fatalf("count %q = %d, want %d", q, got, want)
	}
}
