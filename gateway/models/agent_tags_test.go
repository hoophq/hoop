package models_test

import (
	"testing"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createTaggedAgent(t *testing.T, name string, tags map[string]string) *models.Agent {
	t.Helper()
	require.NoError(t, models.CreateAgentWithTags(models.DB, testOrgID, name, proto.AgentModeStandardType, "hash-"+name, tags))
	agent, err := models.GetAgentByNameOrID(testOrgID, name)
	require.NoError(t, err)
	return agent
}

func listAgentNames(t *testing.T, opts models.AgentFilterOption) []string {
	t.Helper()
	agents, err := models.ListAgents(models.DB, testOrgID, opts)
	require.NoError(t, err)
	var names []string
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

func TestAgentTags(t *testing.T) {
	startTestDB(t)
	prod := createTaggedAgent(t, "agent-prod", map[string]string{
		"hoop.dev/infrastructure.environment": "prod",
		"team":                                "data",
	})
	stg := createTaggedAgent(t, "agent-stg", map[string]string{"hoop.dev/infrastructure.environment": "staging"})
	bare := createTaggedAgent(t, "agent-bare", nil)

	t.Run("create stores the tags of each agent", func(t *testing.T) {
		tags, err := models.GetAgentTags(models.DB, testOrgID, prod.ID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"hoop.dev/infrastructure.environment": "prod", "team": "data"}, tags)

		tags, err = models.GetAgentTags(models.DB, testOrgID, bare.ID)
		require.NoError(t, err)
		assert.NotNil(t, tags)
		assert.Empty(t, tags)

		byAgent, err := models.ListAgentTags(models.DB, testOrgID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"hoop.dev/infrastructure.environment": "staging"}, byAgent[stg.ID])
		assert.NotContains(t, byAgent, bare.ID)
	})

	t.Run("create with a duplicated name keeps the first agent and its tags", func(t *testing.T) {
		err := models.CreateAgentWithTags(models.DB, testOrgID, "agent-prod", proto.AgentModeStandardType, "other", map[string]string{"team": "web"})
		assert.ErrorIs(t, err, models.ErrAlreadyExists)
		tags, err := models.GetAgentTags(models.DB, testOrgID, prod.ID)
		require.NoError(t, err)
		assert.Equal(t, "data", tags["team"])
	})

	t.Run("tag selector filters with the connection semantics", func(t *testing.T) {
		env := "hoop.dev/infrastructure.environment"
		assert.ElementsMatch(t, []string{"agent-prod", "agent-stg", "agent-bare"}, listAgentNames(t, models.AgentFilterOption{}))
		assert.Equal(t, []string{"agent-prod"}, listAgentNames(t, models.AgentFilterOption{TagSelector: env + "=prod"}))
		// != matches an agent that has the key with another value, as /connections does.
		assert.Equal(t, []string{"agent-stg"}, listAgentNames(t, models.AgentFilterOption{TagSelector: env + "!=prod"}))
		assert.Equal(t, []string{"agent-prod"}, listAgentNames(t, models.AgentFilterOption{TagSelector: env + "=prod, team=data"}))
		assert.Empty(t, listAgentNames(t, models.AgentFilterOption{TagSelector: env + "=prod,team!=data"}))
		assert.Empty(t, listAgentNames(t, models.AgentFilterOption{TagSelector: env + "=prod,team=web"}))
	})

	t.Run("tag selector combines with status", func(t *testing.T) {
		require.NoError(t, models.UpdateAgentStatus(testOrgID, stg.ID, models.AgentStatusConnected, nil))
		env := "hoop.dev/infrastructure.environment"
		assert.Equal(t, []string{"agent-stg"}, listAgentNames(t, models.AgentFilterOption{Status: "CONNECTED"}))
		assert.Empty(t, listAgentNames(t, models.AgentFilterOption{Status: "CONNECTED", TagSelector: env + "=prod"}))
		assert.Equal(t, []string{"agent-prod"}, listAgentNames(t, models.AgentFilterOption{Status: "DISCONNECTED", TagSelector: env + "=prod"}))
	})

	t.Run("an invalid tag selector is an error", func(t *testing.T) {
		assert.Error(t, models.ValidateTagSelector("env"))
		_, err := models.ListAgents(models.DB, testOrgID, models.AgentFilterOption{TagSelector: "=prod"})
		assert.Error(t, err)
	})

	t.Run("update replaces the tags and changes no other field", func(t *testing.T) {
		before, err := models.GetAgentByNameOrID(testOrgID, "agent-stg")
		require.NoError(t, err)
		require.NoError(t, models.UpdateAgentTags(models.DB, testOrgID, stg.ID, map[string]string{"team": "web"}))
		tags, err := models.GetAgentTags(models.DB, testOrgID, stg.ID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"team": "web"}, tags)
		after, err := models.GetAgentByNameOrID(testOrgID, "agent-stg")
		require.NoError(t, err)
		assert.Equal(t, before, after)

		require.NoError(t, models.UpdateAgentTags(models.DB, testOrgID, stg.ID, map[string]string{}))
		tags, err = models.GetAgentTags(models.DB, testOrgID, stg.ID)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("update of an unknown agent returns not found", func(t *testing.T) {
		err := models.UpdateAgentTags(models.DB, testOrgID, "00000000-0000-0000-0000-00000000dead", map[string]string{"team": "web"})
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})

	t.Run("in use lists each distinct pair", func(t *testing.T) {
		require.NoError(t, models.UpdateAgentTags(models.DB, testOrgID, bare.ID, map[string]string{"team": "data"}))
		items, err := models.ListAgentTagsInUse(models.DB, testOrgID)
		require.NoError(t, err)
		var pairs []string
		for _, t := range items {
			pairs = append(pairs, t.Key+"="+t.Value)
		}
		assert.Equal(t, []string{"hoop.dev/infrastructure.environment=prod", "team=data"}, pairs)
	})

	t.Run("delete removes the tags and a recreated agent starts with none", func(t *testing.T) {
		require.NoError(t, models.DeleteAgentByNameOrID(testOrgID, "agent-prod"))
		var count int64
		require.NoError(t, models.DB.Raw(`SELECT COUNT(*) FROM private.agent_tags WHERE agent_id = ?`, prod.ID).Scan(&count).Error)
		assert.Zero(t, count)

		again := createTaggedAgent(t, "agent-prod", nil)
		assert.Equal(t, prod.ID, again.ID)
		tags, err := models.GetAgentTags(models.DB, testOrgID, again.ID)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})
}
