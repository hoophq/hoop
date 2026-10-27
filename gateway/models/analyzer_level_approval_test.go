package models_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A human level names its own groups. The managed rule follows them, for an
// older sidecar and for the review limits, and goes once no person holds.
func TestTheManagedRuleFollowsTheHumanLevels(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)
	sync := func(spec string, reviewers *[]string) {
		t.Helper()
		require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, orgID, "levels", json.RawMessage(spec), reviewers))
	}
	groups := func() []string {
		t.Helper()
		got, err := services.AnalyzerApprovalReviewers(models.DB, orgID, "levels")
		require.NoError(t, err)
		return got
	}

	// Created with the level's groups; the spec names no approval_rule.
	sync(`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"," sre ","dba"]}}`, nil)
	assert.Equal(t, []string{"dba", "sre"}, groups())
	rule, err := services.AnalyzerApprovalRule(models.DB, orgID, "levels")
	require.NoError(t, err)
	require.NotNil(t, rule)
	assert.Equal(t, models.AccessTypeSidecar, rule.AccessType)
	require.NotNil(t, rule.MinApprovals)
	assert.Equal(t, 1, *rule.MinApprovals)

	// The groups follow the levels, not a reviewers_groups the request sends.
	sync(`{"medium":{"action":"require_approval","approver":"human","approval_groups":["payments"]}}`, &[]string{"other"})
	assert.Equal(t, []string{"payments"}, groups())

	// Absent reviewers_groups clears nothing.
	sync(`{"medium":{"action":"require_approval","approver":"human","approval_groups":["payments"]}}`, nil)
	assert.Equal(t, []string{"payments"}, groups())

	// Levels with different groups: the highest human level wins.
	sync(`{"high":{"action":"require_approval","approver":"human","approval_groups":["leads"]},`+
		`"low":{"action":"require_approval","approver":"human","approval_groups":["payments"]}}`, nil)
	assert.Equal(t, []string{"leads"}, groups())

	// No groups: the administrators.
	sync(`{"high":{"action":"require_approval","approver":"human"}}`, nil)
	assert.Equal(t, []string{types.GroupAdmin}, groups())

	// The limits stay on the rule while a person holds.
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, orgID, "levels", &pending, nil))
	sync(`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`, nil)
	rule, err = services.AnalyzerApprovalRule(models.DB, orgID, "levels")
	require.NoError(t, err)
	require.NotNil(t, rule.PendingTTLSec)
	assert.Equal(t, 900, *rule.PendingTTLSec)

	// Only the approval agent holds: no rule.
	sync(`{"high":{"action":"require_approval","approver":"ai_agent"}}`, nil)
	assert.Nil(t, groups())
	// An object hold with no approver is the agent's, the ticket default.
	sync(`{"high":{"action":"require_approval","approver":"human"}}`, nil)
	sync(`{"high":{"action":"require_approval"}}`, nil)
	assert.Nil(t, groups())

	// A bare hold under its own rule still counts as a person's.
	sync(`{"high":"require_approval","medium":{"action":"require_approval","approver":"ai_agent"},"approval_rule":"levels"}`,
		&[]string{"dba"})
	assert.Equal(t, []string{"dba"}, groups())
	sync(`{"high":"require_approval","approval_rule":"levels"}`, nil)
	assert.Equal(t, []string{"dba"}, groups(), "absent reviewers_groups keeps the bare hold's groups")

	// A bare hold under the rule beside a human level: one rule serves both,
	// so they must name the same reviewers. Writing the level's groups would
	// hand the bare hold to other people.
	mixed := `{"high":{"action":"require_approval","approver":"human","approval_groups":["sre"]},` +
		`"medium":"require_approval","approval_rule":"levels"}`
	err = services.SyncAnalyzerApprovalRule(models.DB, orgID, "levels", json.RawMessage(mixed), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one approval rule cannot carry both")
	assert.Equal(t, []string{"dba"}, groups(), "the refusal keeps the bare hold's reviewers")
	err = services.SyncAnalyzerApprovalRule(models.DB, orgID, "levels", json.RawMessage(mixed), &[]string{"dba"})
	require.Error(t, err, "reviewers named for the bare hold that differ from the level's")
	assert.Equal(t, []string{"dba"}, groups())
	// The same groups on both: accepted.
	sync(`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},`+
		`"medium":"require_approval","approval_rule":"levels"}`, nil)
	assert.Equal(t, []string{"dba"}, groups())
	// The request moves the bare hold to the level's groups.
	sync(mixed, &[]string{"sre"})
	assert.Equal(t, []string{"sre"}, groups())

	sync(`{"high":"block"}`, nil)
	assert.Nil(t, groups())
}

func TestAnAnalyzerRuleHoldsForAPersonOnly(t *testing.T) {
	for spec, want := range map[string]bool{
		`{"high":"require_approval","approval_rule":"r"}`:                                 true,
		`{"high":{"action":"require_approval","approver":"human"}}`:                       true,
		`{"high":{"action":"require_review","approver":"human","approval_groups":["a"]}}`: true,
		`{"high":{"action":"require_approval","approver":"ai_agent"}}`:                    false,
		`{"high":{"action":"require_approval"}}`:                                          false,
		`{"high":{"action":"warn"}}`:                                                      false,
	} {
		assert.Equal(t, want, services.AnalyzerRuleHolds(json.RawMessage(spec)), spec)
	}
}
