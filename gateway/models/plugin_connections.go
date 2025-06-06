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

func CreatePluginConnection(pc *PluginConnection) error {
	return DB.Table("private.plugin_connections").
		Create(pc).
		Error
}

func UpdatePluginConnection(pc *PluginConnection) error {
	res := DB.Table("private.plugin_connections").
		Where("org_id = ? AND id = ?", pc.OrgID, pc.ID).
		Updates(map[string]any{
			"plugin_id":     pc.PluginID,
			"connection_id": pc.ConnectionID,
			"config":        pc.Config,
			"udpated_at":    pc.UpdatedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func GetPluginConnection(orgID, id string) (*PluginConnection, error) {
	var pluginConn PluginConnection
	err := DB.Table("private.plugin_connections").
		Where(`WHERE pc.org_id = ? AND pc.id = ?`, orgID, id).
		First(&pluginConn).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &pluginConn, err
}

func DeletePluginConnection(orgID, id string) error {
	return DB.Table("private.plugin_connections").
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&PluginConnection{}).
		Error
}
