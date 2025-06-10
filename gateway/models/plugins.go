package models

import (
	"fmt"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"gorm.io/gorm"
)

var defaultPluginNames = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginEditorName,
	plugintypes.PluginSlackName,
	plugintypes.PluginRunbooksName,
	plugintypes.PluginDLPName,
	plugintypes.PluginReviewName,
}

type Plugin struct {
	ID          string              `gorm:"column:id"`
	OrgID       string              `gorm:"column:org_id"`
	Name        string              `gorm:"column:name"`
	Connections []*PluginConnection `gorm:"column:plugin_connections;serializer:json;->"`
	EnvVars     map[string]string   `gorm:"column:envvars;serializer:json;->"`
}

func (p *Plugin) GetName() string               { return p.Name }
func (p *Plugin) GetOrgID() string              { return p.OrgID }
func (p *Plugin) GetEnvVars() map[string]string { return p.EnvVars }

func GetPluginByName(orgID, name string) (*Plugin, error) {
	var p Plugin
	err := DB.Raw(`
			SELECT
				p.id, p.org_id, p.name,
				( SELECT jsonb_agg(
						jsonb_build_object(
							'id', pc.id,
							'org_id', pc.org_id,
							'plugin_id', pc.plugin_id,
							'connection_id', pc.connection_id,
							'connection_name', c.name,
							'config', pc.config,
							'created_at', to_char(pc.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
							'updated_at', to_char(pc.updated_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
						)
					)
					FROM private.plugin_connections AS pc
					INNER JOIN private.connections AS c ON c.id = pc.connection_id
					WHERE pc.plugin_id = p.id AND pc.enabled = 't'
				) AS plugin_connections,
				(SELECT envs FROM private.env_vars WHERE id = p.id) AS envvars,
				p.created_at, p.updated_at
			FROM private.plugins p
			WHERE p.org_id = ? AND p.name = ?`, orgID, name).
		First(&p).
		Error
	switch err {
	case gorm.ErrRecordNotFound:
		return nil, ErrNotFound
	case nil:
		return &p, nil
	default:
		return nil, err
	}
}

func ListPlugins(orgID string) ([]Plugin, error) {
	var items []Plugin
	return items, DB.Raw(`
			SELECT
				p.id, p.org_id, p.name,
				( SELECT jsonb_agg(
						jsonb_build_object(
							'id', pc.id,
							'org_id', pc.org_id,
							'plugin_id', pc.plugin_id,
							'connection_id', pc.connection_id,
							'connection_name', c.name,
							'config', pc.config,
							'created_at', to_char(pc.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
							'updated_at', to_char(pc.updated_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
						)
					)
					FROM private.plugin_connections AS pc
					INNER JOIN private.connections AS c ON c.id = pc.connection_id
					WHERE pc.plugin_id = p.id AND pc.enabled = 't'
				) AS plugin_connections,
				(SELECT envs FROM private.env_vars WHERE id = p.id) AS envvars,
				p.created_at, p.updated_at
			FROM private.plugins p
			WHERE p.org_id = ?`, orgID).
		Find(&items).
		Error
}

func UpsertPlugin(plugin *Plugin) error {
	return upsertPlugin(plugin)
}

func upsertPlugin(plugin *Plugin) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("private.plugins").Save(plugin).Error; err != nil {
			return fmt.Errorf("failed persisting plugin %v, reason=%v", plugin.Name, err)
		}
		if plugin.EnvVars != nil {
			err := tx.Exec(`
			INSERT INTO private.env_vars (id, org_id, envs)
			VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET envs = ?`,
				plugin.ID, plugin.OrgID, plugin.EnvVars, plugin.EnvVars).
				Error
			if err != nil {
				return fmt.Errorf("failed persisting plugin env vars, reason=%v", err)
			}
		}
		// remove all plugin connections before creating new ones
		err := tx.Exec(`
			DELETE FROM private.plugin_connections WHERE org_id = ? AND plugin_id = ?`, plugin.OrgID, plugin.ID).
			Error
		if err != nil {
			return fmt.Errorf("failed deleting plugin connections, reason=%v", err)
		}
		for _, pconn := range plugin.Connections {
			err = tx.Raw(`
			INSERT INTO private.plugin_connections (id, org_id, plugin_id, connection_id, config)
			VALUES (?, ?, ?, ?, ?)
			RETURNING
				(
					SELECT name FROM private.connections
					WHERE id = private.plugin_connections.connection_id
				) AS connection_name`, pconn.ID, pconn.OrgID, pconn.PluginID, pconn.ConnectionID, pconn.Config).
				Scan(&pconn.ConnectionName).
				Error
			if err != nil {
				return fmt.Errorf("failed persisting plugin connection %v, reason=%v", pconn.ConnectionID, err)
			}
		}
		return nil
	})
}
