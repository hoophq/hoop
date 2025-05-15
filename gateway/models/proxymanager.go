package models

import (
	"time"

	"gorm.io/gorm"
)

type ProxyManagerStatusType string

const (
	// ProxyManagerStatusReady indicates the grpc client is ready to  subscribe to a new connection
	ProxyManagerStatusReady ProxyManagerStatusType = "ready"
	// ProxyManagerStatusConnected indicates the client has opened a new session
	ProxyManagerStatusConnected ProxyManagerStatusType = "connected"
	// ProxyManagerStatusDisconnected indicates the grpc client has disconnected
	ProxyManagerStatusDisconnected ProxyManagerStatusType = "disconnected"
)

type ProxyManagerState struct {
	ID                       string                 `gorm:"column:id"`
	OrgID                    string                 `gorm:"column:org_id"`
	Status                   ProxyManagerStatusType `gorm:"column:status"`
	RequestConnectionName    string                 `gorm:"column:connection"`
	RequestPort              string                 `gorm:"column:port"`
	RequestAccessDurationSec int                    `gorm:"column:access_duration"`
	ClientMetadata           map[string]string      `gorm:"column:metadata;serializer:json"`
	ConnectedAt              time.Time              `gorm:"connected_at"`
}

func UpsertProxyManagerState(obj *ProxyManagerState) error {
	return DB.Table("private.proxymanager_state").Save(obj).Error
}

func GetProxyManagerStateByID(orgID, id string) (*ProxyManagerState, error) {
	var state ProxyManagerState
	err := DB.Table("private.proxymanager_state").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&state).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &state, err
}
