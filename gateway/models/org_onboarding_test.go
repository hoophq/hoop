package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

const testAdminGroup = "admin"

func onboardingStatus(t *testing.T) *models.OrgOnboardingStatus {
	t.Helper()
	status, err := models.GetOrgOnboardingStatus(models.DB, testOrgID, testAdminGroup)
	if err != nil {
		t.Fatalf("get onboarding status: %v", err)
	}
	return status
}

func execSQL(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := models.DB.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// connections.resource_name is NOT NULL and FKs into resources(org_id, name).
func seedConnection(t *testing.T, name, accessModeExec string) {
	t.Helper()
	execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, ?, 'database', 'postgres')`, testOrgID, name)
	execSQL(t, `INSERT INTO private.connections (org_id, name, type, resource_name, access_mode_exec)
		VALUES (?, ?, 'postgres', ?, ?::enum_access_status)`, testOrgID, name, name, accessModeExec)
}

// Each step is seeded one at a time so a mistyped column or a subquery wired to
// the wrong table shows up as the wrong boolean flipping, not as a blanket
// failure. Ordering matches the checklist.
func TestGetOrgOnboardingStatus(t *testing.T) {
	startTestDB(t)

	status := onboardingStatus(t)
	if status.AllChecksPass() || status.Completed() {
		t.Fatalf("fresh org must not be complete: %+v", status)
	}
	if status.ExecConnectionName != nil || status.FirstConnectionName != nil {
		t.Fatalf("fresh org must have no connection names: %+v", status)
	}

	steps := []struct {
		name  string
		seed  func()
		check func(*models.OrgOnboardingStatus) bool
	}{
		{
			name: "agent_deployed",
			seed: func() {
				execSQL(t, `INSERT INTO private.agents (org_id, name, mode, key_hash, status)
					VALUES (?, 'agent-one', 'standard', 'hash', 'CONNECTED')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.AgentDeployed },
		},
		{
			name:  "resource_created",
			seed:  func() { seedConnection(t, "conn-disabled", "disabled") },
			check: func(s *models.OrgOnboardingStatus) bool { return s.ResourceCreated },
		},
		{
			name: "session_ran",
			seed: func() {
				execSQL(t, `INSERT INTO private.sessions (org_id, connection, connection_type, verb, status)
					VALUES (?, 'conn-disabled', 'postgres', 'exec', 'done')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.SessionRan },
		},
		{
			name: "groups_created",
			seed: func() {
				execSQL(t, `INSERT INTO private.user_groups (org_id, name) VALUES (?, 'sre')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.GroupsCreated },
		},
		{
			name: "people_assigned",
			seed: func() {
				execSQL(t, `INSERT INTO private.users (org_id, subject, email, name, status)
					VALUES (?, 'onboarding-user', 'onboarding-user@hoop.dev', 'onboarding-user', 'active')`, testOrgID)
				execSQL(t, `INSERT INTO private.user_groups (org_id, user_id, name)
					SELECT ?, id, 'sre' FROM private.users WHERE subject = 'onboarding-user'`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.PeopleAssigned },
		},
		{
			name: "guardrails_explored",
			seed: func() {
				execSQL(t, `INSERT INTO private.guardrail_rules (org_id, name) VALUES (?, 'no-drop')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.GuardrailsExplored },
		},
		{
			name: "data_masking_explored",
			seed: func() {
				execSQL(t, `INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'mask-pii')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.DataMaskingExplored },
		},
		{
			name: "ai_analyzer_enabled",
			seed: func() {
				execSQL(t, `INSERT INTO private.ai_providers (org_id, feature, provider, model)
					VALUES (?, 'session-analyzer', 'openai', 'gpt-4o')`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.AIAnalyzerEnabled },
		},
		{
			name: "protection_level_set",
			seed: func() {
				execSQL(t, `UPDATE private.orgs SET default_protection_profile = 'protection-medium' WHERE id = ?`, testOrgID)
			},
			check: func(s *models.OrgOnboardingStatus) bool { return s.ProtectionLevelSet },
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if step.check(onboardingStatus(t)) {
				t.Fatalf("%s must be false before seeding", step.name)
			}
			step.seed()
			status := onboardingStatus(t)
			if !step.check(status) {
				t.Fatalf("%s must be true after seeding: %+v", step.name, status)
			}
		})
	}

	status = onboardingStatus(t)
	if !status.AllChecksPass() {
		t.Fatalf("every step seeded, expected all checks to pass: %+v", status)
	}
	// The only connection so far has exec disabled, so it must not be offered
	// as the web terminal target.
	if status.ExecConnectionName != nil {
		t.Fatalf("exec-disabled connection must not be a web terminal target, got %q", *status.ExecConnectionName)
	}
	if status.FirstConnectionName == nil || *status.FirstConnectionName != "conn-disabled" {
		t.Fatalf("unexpected first connection name: %+v", status.FirstConnectionName)
	}

	seedConnection(t, "conn-exec", "enabled")
	status = onboardingStatus(t)
	if status.ExecConnectionName == nil || *status.ExecConnectionName != "conn-exec" {
		t.Fatalf("unexpected exec connection name: %+v", status.ExecConnectionName)
	}
}

func TestMarkOrgOnboardingCompleted(t *testing.T) {
	startTestDB(t)

	if onboardingStatus(t).PreviouslyCompleted {
		t.Fatal("fresh org must not be latched")
	}

	if err := models.MarkOrgOnboardingCompleted(models.DB, testOrgID); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	status := onboardingStatus(t)
	if !status.PreviouslyCompleted {
		t.Fatal("expected the org to be latched")
	}
	// No live check passes, yet the org stays complete — the latch is what makes
	// onboarding terminal.
	if status.AllChecksPass() {
		t.Fatalf("no step was seeded, checks must not pass: %+v", status)
	}
	if !status.Completed() {
		t.Fatal("a latched org must report completed")
	}

	org, err := models.GetOrganizationByNameOrID(testOrgID)
	if err != nil {
		t.Fatalf("get org: %v", err)
	}
	if org.OnboardingCompletedAt == nil {
		t.Fatal("expected onboarding_completed_at to be stamped")
	}

	// A second call must not move the timestamp forward.
	first := *org.OnboardingCompletedAt
	if err := models.MarkOrgOnboardingCompleted(models.DB, testOrgID); err != nil {
		t.Fatalf("mark completed twice: %v", err)
	}
	org, err = models.GetOrganizationByNameOrID(testOrgID)
	if err != nil {
		t.Fatalf("get org after second mark: %v", err)
	}
	if !org.OnboardingCompletedAt.Equal(first) {
		t.Fatalf("first write must win, got %v then %v", first, *org.OnboardingCompletedAt)
	}
}
