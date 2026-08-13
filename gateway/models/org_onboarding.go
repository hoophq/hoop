package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/proto"
	"gorm.io/gorm"
)

// These strings are also the API response field names, the webapp's checkKey
// values and the keys in orgs.onboarding_steps — renaming one means all three.
const (
	StepAgentDeployed       = "agent_deployed"
	StepResourceCreated     = "resource_created"
	StepSessionRan          = "session_ran"
	StepGroupsCreated       = "groups_created"
	StepPeopleAssigned      = "people_assigned"
	StepGuardrailsExplored  = "guardrails_explored"
	StepDataMaskingExplored = "data_masking_explored"
	StepAIAnalyzerEnabled   = "ai_analyzer_enabled"
	StepProtectionLevelSet  = "protection_level_set"
)

var OnboardingStepKeys = []string{
	StepAgentDeployed,
	StepResourceCreated,
	StepSessionRan,
	StepGroupsCreated,
	StepPeopleAssigned,
	StepGuardrailsExplored,
	StepDataMaskingExplored,
	StepAIAnalyzerEnabled,
	StepProtectionLevelSet,
}

type OrgOnboardingStatus struct {
	// Effective state per step: satisfied once, satisfied forever.
	Checks map[string]bool

	// Targets for the "Run your first session" shortcut.
	ExecConnectionName  *string
	FirstConnectionName *string

	// The persisted latch, not AllChecksPass. Only a stamp that landed sets it.
	Completed bool

	// Satisfied live, not yet stamped.
	NewlySatisfied []string
}

func (s *OrgOnboardingStatus) AllChecksPass() bool {
	for _, key := range OnboardingStepKeys {
		if !s.Checks[key] {
			return false
		}
	}
	return true
}

type onboardingRow struct {
	LiveChecks          []byte  `gorm:"column:live_checks"`
	LatchedSteps        []byte  `gorm:"column:latched_steps"`
	PreviouslyCompleted bool    `gorm:"column:previously_completed"`
	ExecConnectionName  *string `gorm:"column:exec_connection_name"`
	FirstConnectionName *string `gorm:"column:first_connection_name"`
}

// Every check is an EXISTS so it stops at the first row. Never COUNT here.
//
// Subqueries must filter on @org_id, not the correlated o.id: with o.id the
// planner can't see the value and seq scans private.sessions (~97ms at 2M rows
// vs ~0.1ms).
//
// Live checks are OR'd with orgs.onboarding_steps so a ticked step stays ticked
// after the underlying resource is deleted.
func GetOrgOnboardingStatus(db *gorm.DB, orgID, adminGroupName string) (*OrgOnboardingStatus, error) {
	var row onboardingRow
	err := db.Raw(`
	SELECT
		jsonb_build_object(
			'agent_deployed', EXISTS (
				SELECT 1 FROM private.agents
				WHERE org_id = @org_id AND status = @connected_status AND mode <> @multi_connection_mode
			),
			'resource_created', EXISTS (
				SELECT 1 FROM private.connections WHERE org_id = @org_id
			),
			'session_ran', EXISTS (
				SELECT 1 FROM private.sessions WHERE org_id = @org_id
			),
			'groups_created', EXISTS (
				SELECT 1 FROM private.user_groups
				WHERE org_id = @org_id AND name <> @admin_group
			),
			'people_assigned', EXISTS (
				SELECT 1 FROM private.user_groups
				WHERE org_id = @org_id AND name <> @admin_group AND user_id IS NOT NULL
			),
			-- A protection profile materializes both rule kinds, so picking one
			-- satisfies these two. Profile rules carry managed_by, hence the OR.
			'guardrails_explored', o.default_protection_profile IS NOT NULL OR EXISTS (
				SELECT 1 FROM private.guardrail_rules
				WHERE org_id = @org_id AND managed_by IS NULL
			),
			'data_masking_explored', o.default_protection_profile IS NOT NULL OR EXISTS (
				SELECT 1 FROM private.datamasking_rules
				WHERE org_id = @org_id AND managed_by IS NULL
			),
			'ai_analyzer_enabled', EXISTS (
				SELECT 1 FROM private.ai_providers
				WHERE org_id = @org_id AND feature = @analyzer_feature
			),
			'protection_level_set', o.default_protection_profile IS NOT NULL
		) AS live_checks,
		o.onboarding_steps AS latched_steps,
		o.onboarding_completed_at IS NOT NULL AS previously_completed,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = @org_id AND c.access_mode_exec IS DISTINCT FROM 'disabled'
			ORDER BY c.name ASC LIMIT 1
		) AS exec_connection_name,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = @org_id
			ORDER BY c.name ASC LIMIT 1
		) AS first_connection_name
	FROM private.orgs o
	WHERE o.id = @org_id`,
		map[string]any{
			"org_id":                orgID,
			"connected_status":      string(AgentStatusConnected),
			"multi_connection_mode": proto.AgentModeMultiConnectionType,
			// Only groups created on top of the built-in admin one count.
			"admin_group":      adminGroupName,
			"analyzer_feature": string(AISessionAnalyzerFeature),
		}).
		First(&row).
		Error
	if err != nil {
		return nil, err
	}

	live := map[string]bool{}
	if err := json.Unmarshal(row.LiveChecks, &live); err != nil {
		return nil, fmt.Errorf("decoding onboarding checks: %v", err)
	}
	latched := map[string]json.RawMessage{}
	if len(row.LatchedSteps) > 0 {
		if err := json.Unmarshal(row.LatchedSteps, &latched); err != nil {
			return nil, fmt.Errorf("decoding onboarding steps: %v", err)
		}
	}

	status := &OrgOnboardingStatus{
		Checks:              make(map[string]bool, len(OnboardingStepKeys)),
		ExecConnectionName:  row.ExecConnectionName,
		FirstConnectionName: row.FirstConnectionName,
		Completed:           row.PreviouslyCompleted,
	}
	for _, key := range OnboardingStepKeys {
		_, isLatched := latched[key]
		status.Checks[key] = isLatched || live[key]
		if live[key] && !isLatched {
			status.NewlySatisfied = append(status.NewlySatisfied, key)
		}
	}
	return status, nil
}

func LatchOrgOnboardingSteps(db *gorm.DB, orgID string, steps []string) error {
	stamps := make(map[string]string, len(steps))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, step := range steps {
		stamps[step] = now
	}
	payload, err := json.Marshal(stamps)
	if err != nil {
		return err
	}
	// Stored object on the right so its keys win the merge: a step keeps the
	// timestamp of the first time it was satisfied, even under concurrent reads.
	return db.Exec(`
		UPDATE private.orgs
		SET onboarding_steps = CAST(@payload AS jsonb) || onboarding_steps
		WHERE id = @org_id`,
		map[string]any{"payload": string(payload), "org_id": orgID}).
		Error
}

// First write wins; zero rows affected just means it was already stamped.
func LatchOrgOnboardingCompleted(db *gorm.DB, orgID string) error {
	return db.Table("private.orgs").
		Where("id = ? AND onboarding_completed_at IS NULL", orgID).
		Update("onboarding_completed_at", time.Now().UTC()).
		Error
}
