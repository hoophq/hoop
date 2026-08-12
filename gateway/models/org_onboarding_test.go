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

// latchStatus mirrors what the API handler does: read, then record whatever is
// newly satisfied. Latching only happens on the read path, so a test that never
// calls this only ever sees live checks.
func latchStatus(t *testing.T) *models.OrgOnboardingStatus {
	t.Helper()
	status := onboardingStatus(t)
	if err := models.MarkOrgOnboardingSteps(models.DB, testOrgID, status.NewlySatisfiedSteps()); err != nil {
		t.Fatalf("mark onboarding steps: %v", err)
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
}

// A step is a milestone, not live status: undoing the thing that satisfied it
// must not untick it, otherwise the progress ring runs backwards.
func TestOrgOnboardingStepsLatch(t *testing.T) {
	startTestDB(t)

	if latchStatus(t).Checks[models.StepDataMaskingExplored] {
		t.Fatal("fresh org must not have the data masking step satisfied")
	}

	execSQL(t, `INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'mask-pii')`, testOrgID)
	status := latchStatus(t)
	if !status.Checks[models.StepDataMaskingExplored] {
		t.Fatal("expected the data masking step to be satisfied after adding a rule")
	}
	if len(status.NewlySatisfiedSteps()) == 0 {
		t.Fatal("expected the step to be reported as newly satisfied")
	}

	execSQL(t, `DELETE FROM private.datamasking_rules WHERE org_id = ?`, testOrgID)
	status = onboardingStatus(t)
	if !status.Checks[models.StepDataMaskingExplored] {
		t.Fatalf("deleting the rule must not untick the step: %+v", status.Checks)
	}
	// Already recorded, so there is nothing left to write on later reads.
	if steps := status.NewlySatisfiedSteps(); len(steps) != 0 {
		t.Fatalf("a latched step must not be re-stamped, got %v", steps)
	}

	var stampedAt string
	if err := models.DB.Raw(
		`SELECT onboarding_steps ->> ? FROM private.orgs WHERE id = ?`,
		models.StepDataMaskingExplored, testOrgID).Scan(&stampedAt).Error; err != nil {
		t.Fatalf("read onboarding_steps: %v", err)
	}
	if stampedAt == "" {
		t.Fatal("expected the step to be stamped in orgs.onboarding_steps")
	}

	// A later write for the same step must not move the original timestamp.
	if err := models.MarkOrgOnboardingSteps(models.DB, testOrgID,
		[]string{models.StepDataMaskingExplored}); err != nil {
		t.Fatalf("mark onboarding steps twice: %v", err)
	}
	var secondStampedAt string
	if err := models.DB.Raw(
		`SELECT onboarding_steps ->> ? FROM private.orgs WHERE id = ?`,
		models.StepDataMaskingExplored, testOrgID).Scan(&secondStampedAt).Error; err != nil {
		t.Fatalf("read onboarding_steps again: %v", err)
	}
	if secondStampedAt != stampedAt {
		t.Fatalf("first write must win, got %q then %q", stampedAt, secondStampedAt)
	}
}

// Unknown keys must never reach the column: it is read back as the checklist.
func TestMarkOrgOnboardingStepsIgnoresUnknownKeys(t *testing.T) {
	startTestDB(t)

	if err := models.MarkOrgOnboardingSteps(models.DB, testOrgID, []string{"not_a_step"}); err != nil {
		t.Fatalf("mark unknown step: %v", err)
	}
	var stored string
	if err := models.DB.Raw(
		`SELECT onboarding_steps::text FROM private.orgs WHERE id = ?`, testOrgID).
		Scan(&stored).Error; err != nil {
		t.Fatalf("read onboarding_steps: %v", err)
	}
	if stored != "{}" {
		t.Fatalf("unknown keys must not be stored, got %q", stored)
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
	// No live check passes and no step was ever stamped, yet the org stays
	// complete — the latch is what makes onboarding terminal.
	if status.AllChecksPass() {
		t.Fatalf("no step was seeded, checks must not pass: %+v", status.Checks)
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
