package apiai

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// A tier that names a sidecar rule would gate a session with the reviewer
// policy of a sidecar review.
func TestValidateRiskTierRefusesSidecarRule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	// The embedded backend serves one session at a time.
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	orgID := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'risk-tier-test')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}

	rules := []*models.AccessRequestRule{
		{Name: "sidecar-approvals", AccessType: models.AccessTypeSidecar, ConnectionNames: []string{}},
		{Name: "prod-command", AccessType: models.AccessTypeCommand, ConnectionNames: []string{"pg-prod"}},
	}
	for _, rule := range rules {
		rule.OrgID = orgID
		rule.ApprovalRequiredGroups = []string{}
		rule.ReviewersGroups = []string{"admin"}
		rule.ForceApprovalGroups = []string{}
		rule.MinApprovals = ptr.Int(1)
		if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
			t.Fatalf("seed rule %s: %v", rule.Name, err)
		}
	}

	tier := func(ruleName string) openapi.AISessionAnalyzerRiskTier {
		return openapi.AISessionAnalyzerRiskTier{
			Action:                string(models.RequireAccessRequest),
			AccessRequestRuleName: &ruleName,
		}
	}
	if err := validateRiskTier(orgID, "medium", tier("prod-command")); err != nil {
		t.Errorf("a connection rule must stay valid, got %v", err)
	}
	err = validateRiskTier(orgID, "medium", tier("sidecar-approvals"))
	if err == nil || !strings.Contains(err.Error(), "authorizes sidecars") {
		t.Errorf("expected the sidecar rule to be refused, got %v", err)
	}
}
