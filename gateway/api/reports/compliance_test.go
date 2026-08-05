package apireports

import (
	"math"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

func baseSnapshot() *complianceSnapshot {
	return &complianceSnapshot{
		AuthMethod:     "local",
		GatewayVersion: "unknown",
		SessionMetrics: &models.SessionMetricsAggregatedResult{},
	}
}

func dbConn(name string, masked bool, redactTypes ...string) models.Connection {
	return models.Connection{Name: name, Type: "database", RedactEnabled: masked, RedactTypes: redactTypes}
}

func TestMaskingCoverageThresholds(t *testing.T) {
	tests := []struct {
		name       string
		conns      []models.Connection
		wantStatus openapi.ComplianceStatusType
	}{
		{"no databases", nil, openapi.ComplianceStatusNotApplicable},
		{"full coverage", []models.Connection{dbConn("a", true), dbConn("b", true)}, openapi.ComplianceStatusCompliant},
		{"80 percent coverage", []models.Connection{
			dbConn("a", true), dbConn("b", true), dbConn("c", true), dbConn("d", true), dbConn("e", false),
		}, openapi.ComplianceStatusWarning},
		{"60 percent coverage", []models.Connection{
			dbConn("a", true), dbConn("b", true), dbConn("c", true), dbConn("d", false), dbConn("e", false),
		}, openapi.ComplianceStatusNonCompliant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := baseSnapshot()
			snap.Connections = tt.conns
			got := evalComplianceChecks(snap)["masking_coverage"]
			if got.Status != tt.wantStatus {
				t.Fatalf("masking_coverage = %v, want %v (msg=%q)", got.Status, tt.wantStatus, got.Message)
			}
		})
	}
}

func TestRbacGroupsThresholds(t *testing.T) {
	tests := []struct {
		groups     []string
		wantStatus openapi.ComplianceStatusType
	}{
		{nil, openapi.ComplianceStatusNonCompliant},
		{[]string{"admin"}, openapi.ComplianceStatusWarning},
		{[]string{"admin", "eng", "support"}, openapi.ComplianceStatusCompliant},
	}
	for _, tt := range tests {
		snap := baseSnapshot()
		snap.GroupNames = tt.groups
		got := evalComplianceChecks(snap)["rbac_groups"]
		if got.Status != tt.wantStatus {
			t.Fatalf("rbac_groups with %d groups = %v, want %v", len(tt.groups), got.Status, tt.wantStatus)
		}
	}
}

func TestJitReviewsThresholds(t *testing.T) {
	prodReviewed := models.Connection{Name: "prod-db", Type: "database", Reviewers: []string{"admin"}}
	prodOpen := models.Connection{Name: "prod-api", Type: "application"}

	t.Run("no prod identified", func(t *testing.T) {
		snap := baseSnapshot()
		snap.Connections = []models.Connection{{Name: "staging-db", Type: "database"}}
		got := evalComplianceChecks(snap)["jit_reviews"]
		if got.Status != openapi.ComplianceStatusWarning {
			t.Fatalf("jit_reviews = %v, want warning", got.Status)
		}
	})
	t.Run("full coverage", func(t *testing.T) {
		snap := baseSnapshot()
		snap.Connections = []models.Connection{prodReviewed}
		got := evalComplianceChecks(snap)["jit_reviews"]
		if got.Status != openapi.ComplianceStatusCompliant {
			t.Fatalf("jit_reviews = %v, want compliant", got.Status)
		}
	})
	t.Run("half coverage", func(t *testing.T) {
		snap := baseSnapshot()
		snap.Connections = []models.Connection{prodReviewed, prodOpen}
		got := evalComplianceChecks(snap)["jit_reviews"]
		if got.Status != openapi.ComplianceStatusWarning {
			t.Fatalf("jit_reviews = %v, want warning", got.Status)
		}
	})
	t.Run("low coverage", func(t *testing.T) {
		snap := baseSnapshot()
		snap.Connections = []models.Connection{prodReviewed, prodOpen, prodOpen, prodOpen}
		got := evalComplianceChecks(snap)["jit_reviews"]
		if got.Status != openapi.ComplianceStatusNonCompliant {
			t.Fatalf("jit_reviews = %v, want non_compliant", got.Status)
		}
	})
	t.Run("prod detected via connection tag", func(t *testing.T) {
		snap := baseSnapshot()
		snap.Connections = []models.Connection{{
			Name:           "main-db",
			Type:           "database",
			ConnectionTags: map[string]string{prodEnvironmentTagKey: "prod"},
		}}
		got := evalComplianceChecks(snap)["jit_reviews"]
		if got.Status != openapi.ComplianceStatusNonCompliant {
			t.Fatalf("jit_reviews = %v, want non_compliant (tagged prod without reviewers)", got.Status)
		}
	})
}

func TestAgentHealthThresholds(t *testing.T) {
	agent := func(status string) models.Agent { return models.Agent{Status: status} }
	connected := string(models.AgentStatusConnected)
	disconnected := string(models.AgentStatusDisconnected)

	tests := []struct {
		name       string
		agents     []models.Agent
		wantStatus openapi.ComplianceStatusType
	}{
		{"no agents", nil, openapi.ComplianceStatusWarning},
		{"all connected", []models.Agent{agent(connected), agent(connected)}, openapi.ComplianceStatusCompliant},
		{"90 percent connected", []models.Agent{
			agent(connected), agent(connected), agent(connected), agent(connected), agent(connected),
			agent(connected), agent(connected), agent(connected), agent(connected), agent(disconnected),
		}, openapi.ComplianceStatusWarning},
		{"half connected", []models.Agent{agent(connected), agent(disconnected)}, openapi.ComplianceStatusNonCompliant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := baseSnapshot()
			snap.Agents = tt.agents
			results := evalComplianceChecks(snap)
			if got := results["agents_online"]; got.Status != tt.wantStatus {
				t.Fatalf("agents_online = %v, want %v", got.Status, tt.wantStatus)
			}
			if results["agent_health"] != results["agents_online"] {
				t.Fatalf("agent_health should mirror agents_online")
			}
		})
	}
}

func TestSSOFlipsIdentityChecks(t *testing.T) {
	local := evalComplianceChecks(baseSnapshot())
	for _, id := range []string{"sso_enabled", "unique_user_ids", "auth_method_strength", "mfa_status"} {
		if local[id].Status != openapi.ComplianceStatusWarning {
			t.Fatalf("local auth: %s = %v, want warning", id, local[id].Status)
		}
	}

	snap := baseSnapshot()
	snap.AuthMethod = "oidc"
	sso := evalComplianceChecks(snap)
	for _, id := range []string{"sso_enabled", "unique_user_ids", "auth_method_strength"} {
		if sso[id].Status != openapi.ComplianceStatusCompliant {
			t.Fatalf("oidc auth: %s = %v, want compliant", id, sso[id].Status)
		}
	}
	if sso["mfa_status"].Status != openapi.ComplianceStatusIdpDependent {
		t.Fatalf("oidc auth: mfa_status = %v, want idp_dependent", sso["mfa_status"].Status)
	}
}

func TestChdMaskingTypes(t *testing.T) {
	snap := baseSnapshot()
	snap.Connections = []models.Connection{dbConn("a", true, "CREDIT_CARD_NUMBER")}
	got := evalComplianceChecks(snap)["chd_masking_types"]
	if got.Status != openapi.ComplianceStatusCompliant {
		t.Fatalf("chd_masking_types with card types = %v, want compliant", got.Status)
	}

	snap.Connections = []models.Connection{dbConn("a", true, "EMAIL_ADDRESS")}
	got = evalComplianceChecks(snap)["chd_masking_types"]
	if got.Status != openapi.ComplianceStatusWarning {
		t.Fatalf("chd_masking_types without card types = %v, want warning", got.Status)
	}

	// partial coverage inherits masking_coverage status
	snap.Connections = []models.Connection{dbConn("a", true, "PAN"), dbConn("b", false)}
	results := evalComplianceChecks(snap)
	if results["chd_masking_types"].Status != results["masking_coverage"].Status {
		t.Fatalf("chd_masking_types = %v, want inherited %v",
			results["chd_masking_types"].Status, results["masking_coverage"].Status)
	}
}

func TestCatalogIntegrity(t *testing.T) {
	// 4 identity + 6 access_control + 7 data_protection + 6 audit_trail +
	// 5 monitoring_response + 5 infrastructure = 33 checks.
	if len(complianceChecks) != 33 {
		t.Fatalf("expected 33 checks in catalog, got %d", len(complianceChecks))
	}
	seen := map[string]bool{}
	for _, check := range complianceChecks {
		if seen[check.ID] {
			t.Errorf("duplicate check id %q", check.ID)
		}
		seen[check.ID] = true
	}
	results := evalComplianceChecks(baseSnapshot())
	for _, check := range complianceChecks {
		if _, ok := results[check.ID]; !ok {
			t.Errorf("check %q has no evaluator result", check.ID)
		}
	}
	pseudo := map[string]bool{pseudoCheckIdpDelegated: true, pseudoCheckInfraDelegated: true, pseudoCheckGapNone: true}
	frameworkIDs := map[string]bool{}
	for _, fw := range complianceFrameworks {
		frameworkIDs[fw.ID] = true
		for _, group := range fw.Groups {
			for _, ctrl := range group.Controls {
				_, isCheck := complianceCheckByID[ctrl.CheckID]
				if !isCheck && !pseudo[ctrl.CheckID] {
					t.Errorf("framework %s control %s references unknown check %q", fw.ID, ctrl.ID, ctrl.CheckID)
				}
				switch ctrl.Action.Type {
				case "app", "docs":
					if ctrl.Action.Target == "" {
						t.Errorf("framework %s control %s has %s action without target", fw.ID, ctrl.ID, ctrl.Action.Type)
					}
				case "external", "none":
					if ctrl.Action.Target != "" {
						t.Errorf("framework %s control %s has %s action with unexpected target", fw.ID, ctrl.ID, ctrl.Action.Type)
					}
				default:
					t.Errorf("framework %s control %s has invalid action type %q", fw.ID, ctrl.ID, ctrl.Action.Type)
				}
			}
		}
	}
	for _, id := range []string{"soc2", "gdpr", "pci_dss", "hipaa", "best_practices"} {
		if !frameworkIDs[id] {
			t.Errorf("missing framework %q", id)
		}
	}
}

// recomputeScore independently applies the weighting rule over control rows.
func recomputeScore(controls []openapi.ComplianceControl, scale float64) (score int, applicable int, compliant int) {
	var weight float64
	for _, ctrl := range controls {
		switch ctrl.Status {
		case openapi.ComplianceStatusCompliant:
			weight += 1.0
			applicable++
			compliant++
		case openapi.ComplianceStatusWarning, openapi.ComplianceStatusIdpDependent:
			weight += 0.5
			applicable++
		case openapi.ComplianceStatusNonCompliant:
			applicable++
		}
	}
	if applicable == 0 {
		return 0, 0, compliant
	}
	return int(math.Round(scale * weight / float64(applicable))), applicable, compliant
}

func TestReportScoring(t *testing.T) {
	snap := baseSnapshot()
	snap.AuthMethod = "oidc"
	snap.GroupNames = []string{"admin", "eng", "support"}
	snap.Connections = []models.Connection{
		{Name: "prod-db", Type: "database", RedactEnabled: true, RedactTypes: []string{"PAN"},
			Reviewers: []string{"admin"}, GuardRailRules: []string{"r1"}},
	}
	snap.Agents = []models.Agent{{Status: string(models.AgentStatusConnected)}}
	snap.GuardrailCount = 1
	snap.SessionMetrics = &models.SessionMetricsAggregatedResult{TotalSessions: 10}
	snap.WebhookConfigured = true

	report := buildComplianceReport(snap)

	if len(report.Frameworks) != 5 {
		t.Fatalf("expected 5 frameworks, got %d", len(report.Frameworks))
	}
	if len(report.Categories) != 6 {
		t.Fatalf("expected 6 categories, got %d", len(report.Categories))
	}

	var allControls []openapi.ComplianceControl
	for _, fw := range report.Frameworks {
		var fwControls []openapi.ComplianceControl
		for _, group := range fw.Groups {
			fwControls = append(fwControls, group.Controls...)
		}
		allControls = append(allControls, fwControls...)
		wantScore, wantApplicable, wantCompliant := recomputeScore(fwControls, 100)
		if fw.ScorePercent != wantScore {
			t.Errorf("framework %s score = %d, want %d", fw.ID, fw.ScorePercent, wantScore)
		}
		if fw.TotalApplicable != wantApplicable {
			t.Errorf("framework %s total_applicable = %d, want %d", fw.ID, fw.TotalApplicable, wantApplicable)
		}
		if fw.Compliant != wantCompliant {
			t.Errorf("framework %s compliant = %d, want %d", fw.ID, fw.Compliant, wantCompliant)
		}
	}
	wantScore, wantApplicable, wantCompliant := recomputeScore(allControls, 1000)
	if report.Overall.Score != wantScore {
		t.Errorf("overall score = %d, want %d", report.Overall.Score, wantScore)
	}
	if report.Overall.TotalApplicable != wantApplicable {
		t.Errorf("overall total_applicable = %d, want %d", report.Overall.TotalApplicable, wantApplicable)
	}
	if report.Overall.Compliant != wantCompliant {
		t.Errorf("overall compliant = %d, want %d", report.Overall.Compliant, wantCompliant)
	}
	if report.Overall.Score < 0 || report.Overall.Score > 1000 {
		t.Fatalf("overall score out of range: %d", report.Overall.Score)
	}

	// This snapshot is nearly fully compliant: strong level expected.
	if report.Overall.Level != "strong" {
		t.Errorf("overall level = %q, want strong (score=%d)", report.Overall.Level, report.Overall.Score)
	}

	// Category totals exclude not_applicable/unable_to_verify.
	results := evalComplianceChecks(snap)
	for _, cat := range report.Categories {
		total, compliant := 0, 0
		for _, check := range complianceChecks {
			if check.Category != cat.ID {
				continue
			}
			switch results[check.ID].Status {
			case openapi.ComplianceStatusNotApplicable, openapi.ComplianceStatusUnableToVerify:
				continue
			case openapi.ComplianceStatusCompliant:
				compliant++
			}
			if results[check.ID].Status != openapi.ComplianceStatusNotApplicable &&
				results[check.ID].Status != openapi.ComplianceStatusUnableToVerify {
				total++
			}
		}
		if cat.Total != total || cat.Compliant != compliant {
			t.Errorf("category %s = %d/%d, want %d/%d", cat.ID, cat.Compliant, cat.Total, compliant, total)
		}
	}
}

func TestActionRequired(t *testing.T) {
	snap := baseSnapshot() // local auth, no groups/connections/agents/webhook
	report := buildComplianceReport(snap)

	if len(report.ActionRequired) == 0 {
		t.Fatal("expected action_required entries for a fresh org")
	}
	results := evalComplianceChecks(snap)
	seenWarning := false
	for _, item := range report.ActionRequired {
		res := results[item.ID]
		if res.Status != openapi.ComplianceStatusWarning && res.Status != openapi.ComplianceStatusNonCompliant {
			t.Errorf("action_required contains %s with status %v", item.ID, res.Status)
		}
		if item.Action.Type != "app" && item.Action.Type != "docs" {
			t.Errorf("action_required item %s has non-actionable action type %q", item.ID, item.Action.Type)
		}
		if item.Status == openapi.ComplianceStatusWarning {
			seenWarning = true
		} else if seenWarning {
			t.Errorf("non_compliant item %s appears after a warning item", item.ID)
		}
	}

	// sso_enabled (warning, docs action) must be present on local auth.
	found := false
	for _, item := range report.ActionRequired {
		if item.ID == "sso_enabled" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sso_enabled in action_required for local auth")
	}
}
