package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/proto"
	"gorm.io/gorm"
)

// Setup checklist step keys. The same strings are the JSON field names in the
// API response, the `checkKey` values in the webapp's STEP_DEFS, and the keys
// stored in orgs.onboarding_steps — renaming one means renaming all three.
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

// OnboardingStepKeys is every step the checklist tracks. Iterating this instead
// of listing fields keeps the latch, the completion test and the response in
// sync when a step is added.
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

// OrgOnboardingStatus is the server-computed state of the sidebar setup
// checklist.
type OrgOnboardingStatus struct {
	// Checks is the effective state of each step in OnboardingStepKeys: a step
	// that has ever been satisfied stays satisfied. Nothing here regresses.
	Checks map[string]bool

	// Targets for the "Run your first session" shortcut: the first
	// web-terminal-capable connection, then any connection as a fallback.
	ExecConnectionName  *string
	FirstConnectionName *string

	// Whether orgs.onboarding_completed_at is already stamped.
	PreviouslyCompleted bool

	// Steps satisfied right now but not yet in orgs.onboarding_steps.
	newlySatisfied []string
}

// AllChecksPass reports whether every step has been satisfied at some point.
func (s *OrgOnboardingStatus) AllChecksPass() bool {
	for _, key := range OnboardingStepKeys {
		if !s.Checks[key] {
			return false
		}
	}
	return true
}

// Completed reports whether onboarding is done.
func (s *OrgOnboardingStatus) Completed() bool {
	return s.PreviouslyCompleted || s.AllChecksPass()
}

// NewlySatisfiedSteps is what MarkOrgOnboardingSteps still has to record.
func (s *OrgOnboardingStatus) NewlySatisfiedSteps() []string { return s.newlySatisfied }

type onboardingRow struct {
	LiveChecks          []byte  `gorm:"column:live_checks"`
	LatchedSteps        []byte  `gorm:"column:latched_steps"`
	PreviouslyCompleted bool    `gorm:"column:previously_completed"`
	ExecConnectionName  *string `gorm:"column:exec_connection_name"`
	FirstConnectionName *string `gorm:"column:first_connection_name"`
}

// GetOrgOnboardingStatus computes the setup checklist in a single round trip.
// Every item is an EXISTS subquery so it stops at the first matching row; the
// webapp used to answer them by listing and counting each resource, which made
// "has this org ever run a session?" an unbounded COUNT over private.sessions.
// adminGroupName is types.GroupAdmin, so the group steps only count groups
// created on top of the built-in one.
//
// The live checks are then OR'd with orgs.onboarding_steps, so a step the org
// has already ticked stays ticked even if the underlying resource is deleted.
func GetOrgOnboardingStatus(db *gorm.DB, orgID, adminGroupName string) (*OrgOnboardingStatus, error) {
	var row onboardingRow
	err := db.Raw(`
	SELECT
		jsonb_build_object(
			'agent_deployed', EXISTS (
				SELECT 1 FROM private.agents
				WHERE org_id = o.id AND status = @connected_status AND mode <> @multi_connection_mode
			),
			'resource_created', EXISTS (
				SELECT 1 FROM private.connections WHERE org_id = o.id
			),
			'session_ran', EXISTS (
				SELECT 1 FROM private.sessions WHERE org_id = o.id
			),
			'groups_created', EXISTS (
				SELECT 1 FROM private.user_groups
				WHERE org_id = o.id AND name <> @admin_group
			),
			'people_assigned', EXISTS (
				SELECT 1 FROM private.user_groups
				WHERE org_id = o.id AND name <> @admin_group AND user_id IS NOT NULL
			),
			'guardrails_explored', EXISTS (
				SELECT 1 FROM private.guardrail_rules
				WHERE org_id = o.id AND managed_by IS NULL
			),
			'data_masking_explored', EXISTS (
				SELECT 1 FROM private.datamasking_rules
				WHERE org_id = o.id AND managed_by IS NULL
			),
			'ai_analyzer_enabled', EXISTS (
				SELECT 1 FROM private.ai_providers
				WHERE org_id = o.id AND feature = @analyzer_feature
			),
			'protection_level_set', o.default_protection_profile IS NOT NULL
		) AS live_checks,
		o.onboarding_steps AS latched_steps,
		o.onboarding_completed_at IS NOT NULL AS previously_completed,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = o.id AND c.access_mode_exec IS DISTINCT FROM 'disabled'
			ORDER BY c.name ASC LIMIT 1
		) AS exec_connection_name,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = o.id
			ORDER BY c.name ASC LIMIT 1
		) AS first_connection_name
	FROM private.orgs o
	WHERE o.id = @org_id`,
		map[string]any{
			"org_id":                orgID,
			"connected_status":      string(AgentStatusConnected),
			"multi_connection_mode": proto.AgentModeMultiConnectionType,
			"admin_group":           adminGroupName,
			"analyzer_feature":      string(AISessionAnalyzerFeature),
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
		PreviouslyCompleted: row.PreviouslyCompleted,
	}
	for _, key := range OnboardingStepKeys {
		_, isLatched := latched[key]
		status.Checks[key] = isLatched || live[key]
		if live[key] && !isLatched {
			status.newlySatisfied = append(status.newlySatisfied, key)
		}
	}
	return status, nil
}

// MarkOrgOnboardingSteps records the first time each step was satisfied. Keys
// already present are left alone, so a step keeps its original timestamp.
// Unknown keys are ignored rather than written.
func MarkOrgOnboardingSteps(db *gorm.DB, orgID string, steps []string) error {
	known := map[string]bool{}
	for _, key := range OnboardingStepKeys {
		known[key] = true
	}
	stamps := map[string]string{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, step := range steps {
		if known[step] {
			stamps[step] = now
		}
	}
	if len(stamps) == 0 {
		return nil
	}
	payload, err := json.Marshal(stamps)
	if err != nil {
		return err
	}
	// The stored object goes on the right so its keys win the merge.
	return db.Exec(`
		UPDATE private.orgs
		SET onboarding_steps = CAST(@payload AS jsonb) || onboarding_steps
		WHERE id = @org_id`,
		map[string]any{"payload": string(payload), "org_id": orgID}).
		Error
}
