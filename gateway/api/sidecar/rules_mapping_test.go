package apisidecar

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/sidecar/daemon"
)

const testOrgID = "00000000-0000-0000-0000-0000000000c1"

func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("close embedded database: %v", err)
		}
	})

	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'mapping-test')`, testOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

func TestEnrichSidecarConfiguration(t *testing.T) {
	startTestDB(t)

	orgUUID := uuid.MustParse(testOrgID)
	sidecarID := uuid.NewString()

	// Seed Sidecar
	storedConfig := daemon.Config{
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "mysql"},
			{Name: "postgres-prod", Protocol: "postgres"},
		},
	}
	configBytes, _ := json.Marshal(storedConfig)
	sidecar := models.Sidecar{
		ID:            sidecarID,
		OrgID:         testOrgID,
		Name:          "test-sidecar",
		CreatedBy:     "admin",
		CreatedAt:     time.Now().UTC(),
		Configuration: models.SidecarConfiguration(storedConfig),
	}
	if err := models.DB.Exec(
		`INSERT INTO private.sidecars (id, org_id, name, key_hash, created_by, created_at, configuration) 
		 VALUES (?, ?, ?, 'dummy_hash', 'admin', NOW(), ?)`,
		sidecar.ID, sidecar.OrgID, sidecar.Name, configBytes).Error; err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	// 1. Seed Guardrail Rule
	grRule := models.GuardRailRules{
		ID:          uuid.NewString(),
		OrgID:       testOrgID,
		Name:        "block-drops",
		Description: "Block drop table statements",
		Input: map[string]any{
			"rules": []any{
				map[string]any{
					"type":          "deny_words_list",
					"words":         []string{"DROP"},
					"pattern_regex": "",
					"message":       "DROP statements are not allowed",
				},
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := models.DB.Table("private.guardrail_rules").Create(&grRule).Error; err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}

	// Associate Guardrail to sidecar listener
	grMapping := models.GuardRailRuleSidecar{
		ID:           uuid.NewString(),
		OrgID:        testOrgID,
		RuleID:       grRule.ID,
		SidecarID:    sidecar.ID,
		ListenerName: "postgres-prod",
		CreatedAt:    time.Now().UTC(),
	}
	if err := models.DB.Create(&grMapping).Error; err != nil {
		t.Fatalf("map guardrail rule: %v", err)
	}

	// 2. Seed Data Masking Rule
	dmRule := models.DataMaskingRule{
		ID:          uuid.NewString(),
		OrgID:       testOrgID,
		Name:        "mask-ssn",
		Description: "Mask social security numbers",
		SupportedEntityTypes: []models.SupportedEntityTypesEntry{
			{Name: "SSN", EntityTypes: []string{"US_SSN"}},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := models.DB.Table("private.datamasking_rules").Create(&dmRule).Error; err != nil {
		t.Fatalf("seed data masking rule: %v", err)
	}

	// Associate Data Masking to sidecar listener
	dmMapping := models.DataMaskingRuleSidecar{
		ID:           uuid.NewString(),
		OrgID:        testOrgID,
		RuleID:       dmRule.ID,
		SidecarID:    sidecar.ID,
		ListenerName: "postgres-prod",
		CreatedAt:    time.Now().UTC(),
	}
	if err := models.DB.Create(&dmMapping).Error; err != nil {
		t.Fatalf("map data masking rule: %v", err)
	}

	// 3. Seed AI Session Analyzer Rule
	desc := "Analyze all DB queries for anomalies"
	ruleName := "test-approval-rule"
	aiRule := models.AISessionAnalyzerRules{
		ID:              uuid.New(),
		OrgID:           orgUUID,
		Name:            "analyze-sessions",
		Description:     &desc,
		ConnectionNames: []string{},
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			HighRisk: &models.AISessionAnalyzerRiskTier{Action: models.BlockExecution},
			MediumRisk: &models.AISessionAnalyzerRiskTier{
				Action:                models.RequireAccessRequest,
				AccessRequestRuleName: &ruleName,
			},
			LowRisk: &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := models.DB.Create(&aiRule).Error; err != nil {
		t.Fatalf("seed AI session analyzer rule: %v", err)
	}

	// Associate AI Session Analyzer to sidecar listener
	aiMapping := models.AISessionAnalyzerRuleSidecar{
		ID:             uuid.NewString(),
		OrgID:          testOrgID,
		AnalyzerRuleID: aiRule.ID.String(),
		SidecarID:      sidecar.ID,
		ListenerName:   "postgres-prod",
		CreatedAt:      time.Now().UTC(),
	}
	if err := models.DB.Create(&aiMapping).Error; err != nil {
		t.Fatalf("map AI session analyzer rule: %v", err)
	}

	// Execute Enrichment
	testConfig := daemon.Config{
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "mysql"},
			{Name: "postgres-prod", Protocol: "postgres"},
		},
	}
	err := enrichSidecarConfiguration(testOrgID, sidecar.ID, &testConfig)
	if err != nil {
		t.Fatalf("enrichSidecarConfiguration failed: %v", err)
	}

	// Assertions for unmapped listener (appdb)
	appdb := testConfig.Listeners[0]
	if appdb.Guardrails != nil && len(appdb.Guardrails.Rules) > 0 {
		t.Errorf("appdb listener should not have guardrail rules")
	}
	if appdb.Mask != nil && len(appdb.Mask.Rules) > 0 {
		t.Errorf("appdb listener should not have masking rules")
	}
	if appdb.Analyzer != nil {
		t.Errorf("appdb listener should not have an AI analyzer")
	}

	// Assertions for mapped listener (postgres-prod)
	prod := testConfig.Listeners[1]

	// 1. Guardrail assertions
	if prod.Guardrails == nil {
		t.Fatalf("postgres-prod guardrails block was not initialized")
	}
	if len(prod.Guardrails.Rules) != 1 {
		t.Fatalf("want 1 guardrail rule, got %d", len(prod.Guardrails.Rules))
	}
	if prod.Guardrails.Rules[0].Name != "block-drops" {
		t.Errorf("want guardrail rule 'block-drops', got %q", prod.Guardrails.Rules[0].Name)
	}

	// 2. Data Masking assertions
	if prod.Mask == nil {
		t.Fatalf("postgres-prod masking block was not initialized")
	}
	var alcatrazRules []AlcatrazRule
	if err := json.Unmarshal(prod.Mask.Rules, &alcatrazRules); err != nil {
		t.Fatalf("failed decoding data masking rules: %v", err)
	}
	if len(alcatrazRules) != 1 {
		t.Fatalf("want 1 masking rule, got %d", len(alcatrazRules))
	}
	if alcatrazRules[0].Name != "mask-ssn" {
		t.Errorf("want masking rule name 'mask-ssn', got %q", alcatrazRules[0].Name)
	}

	// 3. AI Session Analyzer assertions
	if prod.Analyzer == nil {
		t.Fatalf("postgres-prod AI analyzer block was not initialized")
	}
	if prod.Analyzer.HighRisk != "block" || prod.Analyzer.MediumRisk != "defer" || prod.Analyzer.LowRisk != "allow" {
		t.Errorf("incorrect AI analyzer risk actions: %+v", prod.Analyzer)
	}
	if prod.Analyzer.ApprovalRule != "test-approval-rule" {
		t.Errorf("want approval rule 'test-approval-rule', got %q", prod.Analyzer.ApprovalRule)
	}
}
