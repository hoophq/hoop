package models

import (
	"time"

	"gorm.io/gorm"
)

// func (p *proxyManager) FetchOne(ctx pgrest.OrgContext, id string) (*types.Client, error) {
// 	var state pgrest.ProxyManagerState
// 	err := pgrest.New("/proxymanager_state?org_id=eq.%s&id=eq.%s", ctx.GetOrgID(), url.QueryEscape(id)).
// 		FetchOne().
// 		DecodeInto(&state)
// 	if err != nil {
// 		if err == pgrest.ErrNotFound {
// 			return nil, nil
// 		}
// 		return nil, err
// 	}
// 	return &types.Client{
// 		ID:                    state.ID,
// 		OrgID:                 state.OrgID,
// 		Status:                types.ClientStatusType(state.Status),
// 		RequestConnectionName: state.Connection,
// 		RequestPort:           state.Port,
// 		RequestAccessDuration: time.Duration(state.AccessDuration) * time.Second,
// 		ClientMetadata:        state.ClientMetadata,
// 		ConnectedAt:           state.GetConnectedAt(),
// 	}, nil
// }

// func (p *proxyManager) Update(ctx pgrest.OrgContext, c *types.Client) error {
// 	return pgrest.New("/proxymanager_state").Upsert(map[string]any{
// 		"id":              c.ID,
// 		"org_id":          ctx.GetOrgID(),
// 		"status":          c.Status,
// 		"connection":      c.RequestConnectionName,
// 		"port":            c.RequestPort,
// 		"access_duration": int(c.RequestAccessDuration.Seconds()),
// 		"metadata":        c.ClientMetadata,
// 		"connected_at":    c.ConnectedAt,
// 	}).Error()
// }

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
