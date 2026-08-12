package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

const testAdminGroup = "admin"

func onboardingStatus(t *testing.T) *models.OrgOnboardingStatus {
	t.Helper()
	status, err := models.SyncOrgOnboardingStatus(models.DB, testOrgID, testAdminGroup)
	if err != nil {
		t.Fatalf("sync onboarding status: %v", err)
	}
	return status
}

func execSQL(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := models.DB.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func queryString(t *testing.T, query string, args ...any) string {
	t.Helper()
	var out string
	if err := models.DB.Raw(query, args...).Scan(&out).Error; err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return out
}

// connections.resource_name is NOT NULL and FKs into resources(org_id, name).
func seedConnection(t *testing.T, name, accessModeExec string) {
	t.Helper()
	execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, ?, 'database', 'postgres')`, testOrgID, name)
	execSQL(t, `INSERT INTO private.connections (org_id, name, type, resource_name, access_mode_exec)
		VALUES (?, ?, 'postgres', ?, ?::enum_access_status)`, testOrgID, name, name, accessModeExec)
}

// Each step is seeded one at a time so a subquery wired to the wrong table
// shows up as the wrong boolean flipping, not as a blanket failure.
func TestOrgOnboardingStatus(t *testing.T) {
	startTestDB(t)

	status := onboardingStatus(t)
	if status.AllChecksPass() || status.Completed() {
		t.Fatalf("fresh org must not be complete: %+v", status.Checks)
	}
	if status.ExecConnectionName != nil || status.FirstConnectionName != nil {
		t.Fatalf("fresh org must have no connection names: %+v", status)
	}

	steps := []struct {
		key  string
		seed func()
	}{
		{
			key: models.StepAgentDeployed,
			seed: func() {
				execSQL(t, `INSERT INTO private.agents (org_id, name, mode, key_hash, status)
					VALUES (?, 'agent-one', 'standard', 'hash', 'CONNECTED')`, testOrgID)
			},
		},
		{
			key:  models.StepResourceCreated,
			seed: func() { seedConnection(t, "conn-disabled", "disabled") },
		},
		{
			key: models.StepSessionRan,
			seed: func() {
				execSQL(t, `INSERT INTO private.sessions (org_id, connection, connection_type, verb, status)
					VALUES (?, 'conn-disabled', 'postgres', 'exec', 'done')`, testOrgID)
			},
		},
		{
			key: models.StepGroupsCreated,
			seed: func() {
				execSQL(t, `INSERT INTO private.user_groups (org_id, name) VALUES (?, 'sre')`, testOrgID)
			},
		},
		{
			key: models.StepPeopleAssigned,
			seed: func() {
				execSQL(t, `INSERT INTO private.users (org_id, subject, email, name, status)
					VALUES (?, 'onboarding-user', 'onboarding-user@hoop.dev', 'onboarding-user', 'active')`, testOrgID)
				execSQL(t, `INSERT INTO private.user_groups (org_id, user_id, name)
					SELECT ?, id, 'sre' FROM private.users WHERE subject = 'onboarding-user'`, testOrgID)
			},
		},
		{
			key: models.StepGuardrailsExplored,
			seed: func() {
				execSQL(t, `INSERT INTO private.guardrail_rules (org_id, name) VALUES (?, 'no-drop')`, testOrgID)
			},
		},
		{
			key: models.StepDataMaskingExplored,
			seed: func() {
				execSQL(t, `INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'mask-pii')`, testOrgID)
			},
		},
		{
			key: models.StepAIAnalyzerEnabled,
			seed: func() {
				execSQL(t, `INSERT INTO private.ai_providers (org_id, feature, provider, model)
					VALUES (?, 'session-analyzer', 'openai', 'gpt-4o')`, testOrgID)
			},
		},
		{
			key: models.StepProtectionLevelSet,
			seed: func() {
				execSQL(t, `UPDATE private.orgs SET default_protection_profile = 'protection-medium' WHERE id = ?`, testOrgID)
			},
		},
	}

	if len(steps) != len(models.OnboardingStepKeys) {
		t.Fatalf("every step in OnboardingStepKeys needs a case here: %d vs %d",
			len(steps), len(models.OnboardingStepKeys))
	}

	for _, step := range steps {
		t.Run(step.key, func(t *testing.T) {
			if onboardingStatus(t).Checks[step.key] {
				t.Fatalf("%s must be false before seeding", step.key)
			}
			step.seed()
			status := onboardingStatus(t)
			if !status.Checks[step.key] {
				t.Fatalf("%s must be true after seeding: %+v", step.key, status.Checks)
			}
		})
	}

	status = onboardingStatus(t)
	if !status.AllChecksPass() {
		t.Fatalf("every step seeded, expected all checks to pass: %+v", status.Checks)
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

	if queryString(t, `SELECT COALESCE(onboarding_completed_at::text, '')
		FROM private.orgs WHERE id = ?`, testOrgID) == "" {
		t.Fatal("expected onboarding_completed_at to be stamped once every check passed")
	}
	// Completed must track the stamp, not the live checks, or it would disagree
	// with show_setup_checklist on /userinfo.
	if !status.Completed() {
		t.Fatal("expected completed once the latch was stamped")
	}
}

// Undoing the thing that satisfied a step must not untick it, otherwise the
// progress ring runs backwards.
func TestOrgOnboardingStepsLatch(t *testing.T) {
	startTestDB(t)

	if onboardingStatus(t).Checks[models.StepDataMaskingExplored] {
		t.Fatal("fresh org must not have the data masking step satisfied")
	}

	execSQL(t, `INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'mask-pii')`, testOrgID)
	if !onboardingStatus(t).Checks[models.StepDataMaskingExplored] {
		t.Fatal("expected the data masking step to be satisfied after adding a rule")
	}

	stampedAt := queryString(t, `SELECT COALESCE(onboarding_steps ->> ?, '')
		FROM private.orgs WHERE id = ?`, models.StepDataMaskingExplored, testOrgID)
	if stampedAt == "" {
		t.Fatal("expected the step to be stamped in orgs.onboarding_steps")
	}

	execSQL(t, `DELETE FROM private.datamasking_rules WHERE org_id = ?`, testOrgID)
	status := onboardingStatus(t)
	if !status.Checks[models.StepDataMaskingExplored] {
		t.Fatalf("deleting the rule must not untick the step: %+v", status.Checks)
	}

	// Re-satisfying it later must not move the original timestamp.
	execSQL(t, `INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'mask-pii-again')`, testOrgID)
	onboardingStatus(t)
	secondStampedAt := queryString(t, `SELECT COALESCE(onboarding_steps ->> ?, '')
		FROM private.orgs WHERE id = ?`, models.StepDataMaskingExplored, testOrgID)
	if secondStampedAt != stampedAt {
		t.Fatalf("first write must win, got %q then %q", stampedAt, secondStampedAt)
	}
}

// The completion latch is what keeps an org done when the checklist grows a new
// step it never satisfied. Simulates that: stamped completion, no steps latched.
func TestOrgOnboardingCompletionIsTerminal(t *testing.T) {
	startTestDB(t)

	if onboardingStatus(t).Completed() {
		t.Fatal("fresh org must not be complete")
	}

	execSQL(t, `UPDATE private.orgs SET onboarding_completed_at = now() WHERE id = ?`, testOrgID)
	status := onboardingStatus(t)
	if status.AllChecksPass() {
		t.Fatalf("no step was seeded, checks must not pass: %+v", status.Checks)
	}
	if !status.Completed() {
		t.Fatal("a latched org must report completed")
	}
}
