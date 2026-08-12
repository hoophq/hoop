package models

import (
	"github.com/hoophq/hoop/common/proto"
	"gorm.io/gorm"
)

// OrgOnboardingStatus is the server-computed state of the sidebar setup
// checklist. Each boolean maps 1:1 to a sub-item in the webapp's STEP_DEFS.
type OrgOnboardingStatus struct {
	AgentDeployed       bool `gorm:"column:agent_deployed"`
	ResourceCreated     bool `gorm:"column:resource_created"`
	SessionRan          bool `gorm:"column:session_ran"`
	GroupsCreated       bool `gorm:"column:groups_created"`
	PeopleAssigned      bool `gorm:"column:people_assigned"`
	GuardrailsExplored  bool `gorm:"column:guardrails_explored"`
	DataMaskingExplored bool `gorm:"column:data_masking_explored"`
	AIAnalyzerEnabled   bool `gorm:"column:ai_analyzer_enabled"`
	ProtectionLevelSet  bool `gorm:"column:protection_level_set"`

	// Targets for the "Run your first session" shortcut: the first
	// web-terminal-capable connection, then any connection as a fallback.
	ExecConnectionName  *string `gorm:"column:exec_connection_name"`
	FirstConnectionName *string `gorm:"column:first_connection_name"`

	// Whether orgs.onboarding_completed_at is already stamped.
	PreviouslyCompleted bool `gorm:"column:previously_completed"`
}

// AllChecksPass reports whether every checklist item is satisfied right now.
func (s OrgOnboardingStatus) AllChecksPass() bool {
	return s.AgentDeployed &&
		s.ResourceCreated &&
		s.SessionRan &&
		s.GroupsCreated &&
		s.PeopleAssigned &&
		s.GuardrailsExplored &&
		s.DataMaskingExplored &&
		s.AIAnalyzerEnabled &&
		s.ProtectionLevelSet
}

// Completed reports whether onboarding is done. The latch wins over the live
// checks, which can regress once an org is already set up.
func (s OrgOnboardingStatus) Completed() bool {
	return s.PreviouslyCompleted || s.AllChecksPass()
}

// GetOrgOnboardingStatus computes the setup checklist in a single round trip.
// Every item is an EXISTS subquery so it stops at the first matching row; the
// webapp used to answer them by listing and counting each resource, which made
// "has this org ever run a session?" an unbounded COUNT over private.sessions.
// adminGroupName is types.GroupAdmin, so the group steps only count groups
// created on top of the built-in one.
func GetOrgOnboardingStatus(db *gorm.DB, orgID, adminGroupName string) (*OrgOnboardingStatus, error) {
	var status OrgOnboardingStatus
	err := db.Raw(`
	SELECT
		EXISTS (
			SELECT 1 FROM private.agents
			WHERE org_id = @org_id AND status = @connected_status AND mode <> @multi_connection_mode
		) AS agent_deployed,
		EXISTS (
			SELECT 1 FROM private.connections WHERE org_id = @org_id
		) AS resource_created,
		EXISTS (
			SELECT 1 FROM private.sessions WHERE org_id = @org_id
		) AS session_ran,
		EXISTS (
			SELECT 1 FROM private.user_groups
			WHERE org_id = @org_id AND name <> @admin_group
		) AS groups_created,
		EXISTS (
			SELECT 1 FROM private.user_groups
			WHERE org_id = @org_id AND name <> @admin_group AND user_id IS NOT NULL
		) AS people_assigned,
		EXISTS (
			SELECT 1 FROM private.guardrail_rules
			WHERE org_id = @org_id AND managed_by IS NULL
		) AS guardrails_explored,
		EXISTS (
			SELECT 1 FROM private.datamasking_rules
			WHERE org_id = @org_id AND managed_by IS NULL
		) AS data_masking_explored,
		EXISTS (
			SELECT 1 FROM private.ai_providers
			WHERE org_id = @org_id AND feature = @analyzer_feature
		) AS ai_analyzer_enabled,
		EXISTS (
			SELECT 1 FROM private.orgs
			WHERE id = @org_id AND default_protection_profile IS NOT NULL
		) AS protection_level_set,
		EXISTS (
			SELECT 1 FROM private.orgs
			WHERE id = @org_id AND onboarding_completed_at IS NOT NULL
		) AS previously_completed,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = @org_id AND c.access_mode_exec IS DISTINCT FROM 'disabled'
			ORDER BY c.name ASC LIMIT 1
		) AS exec_connection_name,
		(
			SELECT c.name FROM private.connections c
			WHERE c.org_id = @org_id
			ORDER BY c.name ASC LIMIT 1
		) AS first_connection_name`,
		map[string]any{
			"org_id":                orgID,
			"connected_status":      string(AgentStatusConnected),
			"multi_connection_mode": proto.AgentModeMultiConnectionType,
			"admin_group":           adminGroupName,
			"analyzer_feature":      string(AISessionAnalyzerFeature),
		}).
		First(&status).
		Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}
