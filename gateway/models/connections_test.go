package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeleteConnection(t *testing.T) {
	// Setup test database
	orgID := uuid.NewString()
	connID := uuid.NewString()
	connName := "test-connection"

	// Create test connection
	conn := &Connection{
		ID:                 connID,
		OrgID:              orgID,
		Name:               connName,
		Type:               "database",
		SubType:            "postgresql",
		Status:             ConnectionStatusOffline,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "enabled",
		Command:            pq.StringArray{"psql", "-h", "localhost"},
		Envs:               map[string]string{"envvar:DB": "testdb"},
		Tags:               pq.StringArray{"test", "database"},
		ConnectionTags:     map[string]string{"environment": "test"},
		GuardRailRules:     pq.StringArray{"rule1", "rule2"},
		Reviewers:          pq.StringArray{"user1"},
		RedactTypes:        pq.StringArray{"email", "phone"},
	}

	// Test setup - create connection and related data
	t.Run("setup", func(t *testing.T) {
		// Create connection
		err := DB.Table(tableConnections).Create(conn).Error
		require.NoError(t, err)

		// Create environment variables
		err = DB.Table("private.env_vars").Create(&EnvVars{
			ID:    connID,
			OrgID: orgID,
			Envs:  conn.Envs,
		}).Error
		require.NoError(t, err)

		// Create plugin connections
		err = DB.Exec(`INSERT INTO private.plugins (org_id, name) VALUES (?, 'review') ON CONFLICT DO NOTHING`, orgID).Error
		require.NoError(t, err)
		err = DB.Exec(`INSERT INTO private.plugins (org_id, name) VALUES (?, 'dlp') ON CONFLICT DO NOTHING`, orgID).Error
		require.NoError(t, err)

		var reviewPluginID, dlpPluginID string
		err = DB.Raw(`SELECT id FROM private.plugins WHERE org_id = ? AND name = 'review'`, orgID).First(&reviewPluginID).Error
		require.NoError(t, err)
		err = DB.Raw(`SELECT id FROM private.plugins WHERE org_id = ? AND name = 'dlp'`, orgID).First(&dlpPluginID).Error
		require.NoError(t, err)

		err = DB.Exec(`INSERT INTO private.plugin_connections (org_id, plugin_id, connection_id, config) VALUES (?, ?, ?, ?)`,
			orgID, reviewPluginID, connID, pq.StringArray{"user1"}).Error
		require.NoError(t, err)
		err = DB.Exec(`INSERT INTO private.plugin_connections (org_id, plugin_id, connection_id, config) VALUES (?, ?, ?, ?)`,
			orgID, dlpPluginID, connID, pq.StringArray{"email", "phone"}).Error
		require.NoError(t, err)

		// Create connection tags
		err = DB.Exec(`INSERT INTO private.connection_tags (org_id, key, value) VALUES (?, 'environment', 'test')`, orgID).Error
		require.NoError(t, err)
		var tagID string
		err = DB.Raw(`SELECT id FROM private.connection_tags WHERE org_id = ? AND key = 'environment'`, orgID).First(&tagID).Error
		require.NoError(t, err)
		err = DB.Exec(`INSERT INTO private.connection_tags_association (connection_id, tag_id) VALUES (?, ?)`, connID, tagID).Error
		require.NoError(t, err)

		// Create guard rail rules connections
		err = DB.Exec(`INSERT INTO private.guardrail_rules (org_id, name, input, output) VALUES (?, 'rule1', '{}', '{}')`, orgID).Error
		require.NoError(t, err)
		err = DB.Exec(`INSERT INTO private.guardrail_rules (org_id, name, input, output) VALUES (?, 'rule2', '{}', '{}')`, orgID).Error
		require.NoError(t, err)

		var rule1ID, rule2ID string
		err = DB.Raw(`SELECT id FROM private.guardrail_rules WHERE org_id = ? AND name = 'rule1'`, orgID).First(&rule1ID).Error
		require.NoError(t, err)
		err = DB.Raw(`SELECT id FROM private.guardrail_rules WHERE org_id = ? AND name = 'rule2'`, orgID).First(&rule2ID).Error
		require.NoError(t, err)

		err = DB.Exec(`INSERT INTO private.guardrail_rules_connections (org_id, connection_id, rule_id) VALUES (?, ?, ?)`, orgID, connID, rule1ID).Error
		require.NoError(t, err)
		err = DB.Exec(`INSERT INTO private.guardrail_rules_connections (org_id, connection_id, rule_id) VALUES (?, ?, ?)`, orgID, connID, rule2ID).Error
		require.NoError(t, err)
	})

	// Test deletion
	t.Run("delete connection", func(t *testing.T) {
		err := DeleteConnection(orgID, connName)
		require.NoError(t, err)

		// Verify connection is deleted
		var conn Connection
		err = DB.Table(tableConnections).Where(`org_id = ? and name = ?`, orgID, connName).First(&conn).Error
		assert.Error(t, err)
		assert.Equal(t, gorm.ErrRecordNotFound, err)

		// Verify environment variables are deleted
		var envVars EnvVars
		err = DB.Table("private.env_vars").Where(`id = ?`, connID).First(&envVars).Error
		assert.Error(t, err)
		assert.Equal(t, gorm.ErrRecordNotFound, err)

		// Verify plugin connections are deleted
		var pluginConnCount int64
		err = DB.Table("private.plugin_connections").Where(`org_id = ? AND connection_id = ?`, orgID, connID).Count(&pluginConnCount).Error
		require.NoError(t, err)
		assert.Equal(t, int64(0), pluginConnCount)

		// Verify connection tags associations are deleted
		var tagAssocCount int64
		err = DB.Table("private.connection_tags_association").Where(`connection_id = ?`, connID).Count(&tagAssocCount).Error
		require.NoError(t, err)
		assert.Equal(t, int64(0), tagAssocCount)

		// Verify guard rail rules connections are deleted (should be automatic due to CASCADE)
		var guardRailConnCount int64
		err = DB.Table("private.guardrail_rules_connections").Where(`connection_id = ?`, connID).Count(&guardRailConnCount).Error
		require.NoError(t, err)
		assert.Equal(t, int64(0), guardRailConnCount)

		// Verify that the guard rail rules themselves are not deleted (only the associations)
		var guardRailRulesCount int64
		err = DB.Table("private.guardrail_rules").Where(`org_id = ?`, orgID).Count(&guardRailRulesCount).Error
		require.NoError(t, err)
		assert.Equal(t, int64(2), guardRailRulesCount)

		// Verify that the connection tags themselves are not deleted (only the associations)
		var connectionTagsCount int64
		err = DB.Table("private.connection_tags").Where(`org_id = ?`, orgID).Count(&connectionTagsCount).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), connectionTagsCount)
	})

	// Test deletion of non-existent connection
	t.Run("delete non-existent connection", func(t *testing.T) {
		err := DeleteConnection(orgID, "non-existent")
		assert.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	// Test deletion with wrong org ID
	t.Run("delete connection with wrong org ID", func(t *testing.T) {
		// Create a connection with different org ID
		otherOrgID := uuid.NewString()
		otherConnID := uuid.NewString()
		otherConnName := "other-connection"

		err := DB.Table(tableConnections).Create(&Connection{
			ID:                 otherConnID,
			OrgID:              otherOrgID,
			Name:               otherConnName,
			Type:               "database",
			Status:             ConnectionStatusOffline,
			AccessModeRunbooks: "enabled",
			AccessModeExec:     "enabled",
			AccessModeConnect:  "enabled",
			AccessSchema:       "enabled",
		}).Error
		require.NoError(t, err)

		// Try to delete with wrong org ID
		err = DeleteConnection(orgID, otherConnName)
		assert.Error(t, err)
		assert.Equal(t, ErrNotFound, err)

		// Verify the connection still exists
		var conn Connection
		err = DB.Table(tableConnections).Where(`org_id = ? and name = ?`, otherOrgID, otherConnName).First(&conn).Error
		assert.NoError(t, err)

		// Clean up
		err = DB.Table(tableConnections).Where(`org_id = ? and name = ?`, otherOrgID, otherConnName).Delete(&Connection{}).Error
		require.NoError(t, err)
	})
}
