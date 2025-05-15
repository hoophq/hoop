package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Plugin struct {
	ID          string              `gorm:"column:id"`
	OrgID       string              `gorm:"column:org_id"`
	Name        string              `gorm:"column:name"`
	Connections []*PluginConnection `gorm:"column:plugin_connections;serializer:json;->"`
	EnvVars     map[string]string   `gorm:"column:envvars;serializer:json;->"`
}

type PluginConnection struct {
	ID             string         `gorm:"column:id" json:"id"`
	OrgID          string         `gorm:"column:org_id" json:"org_id"`
	PluginID       string         `gorm:"column:plugin_id" json:"plugin_id"`
	ConnectionID   string         `gorm:"column:connection_id" json:"connection_id"`
	ConnectionName string         `gorm:"column:connection_name;->" json:"connection_name"`
	Enabled        bool           `gorm:"column:enabled" json:"enabled"`
	Config         pq.StringArray `gorm:"column:config;type:text[]" json:"config"`
	CreatedAt      time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at" json:"updated_at"`
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

// AddPluginConnection will add or remove a plugin based on the plugin connection config.
// In case it doesn't include any configuration, the plugin connection will be removed
// otherwise it will be created
func AddPluginConnection(orgID, pluginName, connID string, connConfig []string) error {
	existentPlugin, err := GetPluginByName(orgID, pluginName)
	if err != nil {
		return err
	}

	var pluginConnections []*PluginConnection
	var exists bool
	for _, conn := range existentPlugin.Connections {
		if conn.ConnectionID == connID {
			// remove the plugin connection in case
			// the connection config is empty
			exists = true
			if len(connConfig) == 0 {
				continue
			}
			// mutate the config field if the plugin connection exits
			conn.Config = connConfig
		}
		pluginConnections = append(pluginConnections, conn)
	}
	existentPlugin.Connections = pluginConnections
	if !exists && len(connConfig) > 0 {
		existentPlugin.Connections = append(existentPlugin.Connections, &PluginConnection{
			ID:             uuid.NewString(),
			OrgID:          existentPlugin.OrgID,
			PluginID:       existentPlugin.ID,
			ConnectionID:   connID,
			ConnectionName: "", // read only
			Enabled:        true,
			Config:         connConfig,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		})
	}
	return upsertPlugin(existentPlugin)
}

var defaultPluginNames = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginEditorName,
	plugintypes.PluginSlackName,
	plugintypes.PluginRunbooksName,
	plugintypes.PluginDLPName,
	plugintypes.PluginReviewName,
}

func ActivateDefaultPlugins(orgID, connID string) {
	for _, name := range defaultPluginNames {
		pl, err := GetPluginByName(orgID, name)
		if err != nil && err != ErrNotFound {
			log.Warnf("failed fetching plugin %v, reason=%v", name, err)
		}
		var connConfig []string
		if name == plugintypes.PluginDLPName {
			connConfig = proto.DefaultInfoTypes
		}
		if pl == nil {
			pluginID := uuid.NewString()
			newPlugin := &Plugin{
				ID:    pluginID,
				OrgID: orgID,
				Name:  name,
				Connections: []*PluginConnection{{
					ID:             uuid.NewString(),
					OrgID:          orgID,
					PluginID:       pluginID,
					ConnectionID:   connID,
					ConnectionName: "", // read only
					Enabled:        true,
					Config:         connConfig,
					CreatedAt:      time.Now().UTC(),
					UpdatedAt:      time.Now().UTC(),
				}},
				EnvVars: nil,
			}
			if err := upsertPlugin(newPlugin); err != nil {
				log.Warnf("failed creating plugin %v, reason=%v", name, err)
			}
			continue
		}

		var enabled bool
		for _, conn := range pl.Connections {
			if conn.ConnectionID == connID {
				enabled = true
				break
			}
		}
		if !enabled {
			pl.Connections = append(pl.Connections, &PluginConnection{
				ID:             uuid.NewString(),
				OrgID:          pl.OrgID,
				PluginID:       pl.ID,
				ConnectionID:   connID,
				ConnectionName: "", // read only
				Enabled:        true,
				Config:         connConfig,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			})
			if err := upsertPlugin(pl); err != nil {
				log.Warnf("failed enabling plugin %v, reason=%v", pl.Name, err)
			}
		}
	}
}

func filterPluginConnection(pluginConnList []*PluginConnection, connID string) []*PluginConnection {
	filtered := []*PluginConnection{}
	for _, conn := range pluginConnList {
		if conn.ConnectionID != connID {
			filtered = append(filtered, conn)
		}
	}
	return filtered
}
