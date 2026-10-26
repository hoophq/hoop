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

	sync(`{"high":"block"}`, nil)
	assert.Nil(t, groups())
}

// A hand-off level whose agent can select a human hold keeps the managed
// rule: the hand-off reviews read its limits.
func TestTheManagedRuleFollowsANestedHumanHold(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)
	sync := func(spec string) {
		t.Helper()
		require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, orgID, "handoff", json.RawMessage(spec), nil))
	}
	groups := func() []string {
		t.Helper()
		got, err := services.AnalyzerApprovalReviewers(models.DB, orgID, "handoff")
		require.NoError(t, err)
		return got
	}

	sync(`{"medium":{"action":"ai-handoff","allowed_actions":{"warn":{},` +
		`"require_approval":{"approver":"human","approval_groups":["dba"," sre "]}}}}`)
	assert.Equal(t, []string{"dba", "sre"}, groups())

	// The limits stay while the nested hold is there.
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, orgID, "handoff", &pending, nil))
	sync(`{"medium":{"action":"ai-handoff","allowed_actions":{"block":{},` +
		`"require_approval":{"approver":"human","approval_groups":["dba"]}}}}`)
	rule, err := services.AnalyzerApprovalRule(models.DB, orgID, "handoff")
	require.NoError(t, err)
	require.NotNil(t, rule)
	require.NotNil(t, rule.PendingTTLSec)
	assert.Equal(t, 900, *rule.PendingTTLSec)
	assert.Equal(t, []string{"dba"}, groups())

	// The highest human hold wins, direct or nested, as between levels.
	sync(`{"high":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human","approval_groups":["leads"]}}},` +
		`"medium":{"action":"require_approval","approver":"human","approval_groups":["payments"]}}`)
	assert.Equal(t, []string{"leads"}, groups())
	sync(`{"high":{"action":"require_approval","approver":"human","approval_groups":["payments"]},` +
		`"low":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human","approval_groups":["leads"]}}}}`)
	assert.Equal(t, []string{"payments"}, groups())

	// No groups: the administrators.
	sync(`{"high":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human"}}}}`)
	assert.Equal(t, []string{types.GroupAdmin}, groups())

	// A hand-off whose hold is the approval agent's, or that cannot hold,
	// needs no person: no rule.
	sync(`{"high":{"action":"ai-handoff","allowed_actions":["allow","require_approval"]}}`)
	assert.Nil(t, groups())
	sync(`{"high":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human"}}}}`)
	sync(`{"high":{"action":"ai-handoff","allowed_actions":["allow","block"]}}`)
	assert.Nil(t, groups())
	sync(`{"high":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human"}}}}`)
	sync(`{"high":"ai-handoff"}`)
	assert.Nil(t, groups(), "all four allowed holds for the approval agent")
}

func TestAnAnalyzerRuleHoldsForAPersonOnly(t *testing.T) {
	for spec, want := range map[string]bool{
		`{"high":"require_approval","approval_rule":"r"}`:                                              true,
		`{"high":{"action":"require_approval","approver":"human"}}`:                                    true,
		`{"high":{"action":"require_review","approver":"human","approval_groups":["a"]}}`:              true,
		`{"high":{"action":"require_approval","approver":"ai_agent"}}`:                                 false,
		`{"high":{"action":"require_approval"}}`:                                                       false,
		`{"high":{"action":"warn"}}`:                                                                   false,
		`{"high":{"action":"ai-handoff","allowed_actions":{"require_approval":{"approver":"human"}}}}`: true,
		`{"high":{"action":"ai-handoff","allowed_actions":["allow","require_approval"]}}`:              false,
		`{"high":{"action":"ai-handoff","allowed_actions":["allow","block"]}}`:                         false,
	} {
		assert.Equal(t, want, services.AnalyzerRuleHolds(json.RawMessage(spec)), spec)
	}
}
