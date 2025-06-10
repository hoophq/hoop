package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

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

// UpsertPluginConnection updates an existing plugin connection by the plugin ID and connection ID.
func UpsertPluginConnection(orgID, pluginName, connID string, config pq.StringArray) (*PluginConnection, error) {
	var updatedPlugiConn PluginConnection
	return &updatedPlugiConn, DB.Transaction(func(tx *gorm.DB) error {
		var pluginID string
		err := tx.Raw(`SELECT id FROM private.plugins WHERE org_id = ? AND name = ?`, orgID, pluginName).
			First(&pluginID).
			Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrNotFound
			}
			return err
		}
		res := tx.Raw(`
		INSERT INTO private.plugin_connections (org_id, plugin_id, connection_id, config, updated_at)
		VALUES (@org_id, @plugin_id, @connection_id, @config, @updated_at)
		ON CONFLICT (plugin_id, connection_id)
		DO UPDATE SET config = @config, updated_at = @updated_at
		RETURNING *
		`, map[string]any{
			"org_id":        orgID,
			"plugin_id":     pluginID,
			"connection_id": connID,
			"config":        config,
			"updated_at":    time.Now().UTC(),
		}).
			Scan(&updatedPlugiConn)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func GetPluginConnection(orgID, pluginName, connID string) (*PluginConnection, error) {
	var pluginConn PluginConnection
	err := DB.Raw(`
		SELECT pc.id, pc.org_id, pc.plugin_id, pc.connection_id, pc.config, pc.created_at, pc.updated_at
		FROM private.plugin_connections pc
		INNER JOIN private.plugins p ON pc.plugin_id = p.id
		WHERE pc.org_id = ? AND connection_id = ? AND p.name = ?`,
		orgID, connID, pluginName).
		First(&pluginConn).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &pluginConn, err
}

func DeletePluginConnection(orgID, pluginName, connID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var pluginID string
		err := tx.Raw(`SELECT id FROM private.plugins WHERE org_id = ? AND name = ?`, orgID, pluginName).
			First(&pluginID).
			Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrNotFound
			}
			return err
		}
		res := tx.Table("private.plugin_connections").
			Where("org_id = ? AND plugin_id = ? AND connection_id = ?", orgID, pluginID, connID).
			Delete(&PluginConnection{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
