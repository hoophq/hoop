package pgproxymanager

import (
	"net/url"
	"time"

	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type proxyManager struct{}

func New() *proxyManager { return &proxyManager{} }

func (p *proxyManager) FetchOne(ctx pgrest.OrgContext, id string) (*types.Client, error) {
	var state pgrest.ProxyManagerState
	err := pgrest.New("/proxymanager_state?org_id=eq.%s&id=eq.%s", ctx.GetOrgID(), url.QueryEscape(id)).
		FetchOne().
		DecodeInto(&state)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.Client{
		ID:                       state.ID,
		OrgID:                    state.OrgID,
		Status:                   types.ClientStatusType(state.Status),
		RequestConnectionName:    state.Connection,
		RequestConnectionType:    state.ConnectionType,
		RequestConnectionSubType: state.ConnectionSubType,
		RequestPort:              state.Port,
		RequestAccessDuration:    time.Duration(state.AccessDuration) * time.Second,
		ClientMetadata:           state.ClientMetadata,
		ConnectedAt:              state.GetConnectedAt(),
	}, nil
}

func (p *proxyManager) Update(ctx pgrest.OrgContext, c *types.Client) error {
	return pgrest.New("/proxymanager_state").Upsert(map[string]any{
		"id":                 c.ID,
		"org_id":             ctx.GetOrgID(),
		"status":             c.Status,
		"connection":         c.RequestConnectionName,
		"connection_type":    c.RequestConnectionType,
		"connection_subtype": c.RequestConnectionSubType,
		"port":               c.RequestPort,
		"access_duration":    int(c.RequestAccessDuration.Seconds()),
		"metadata":           c.ClientMetadata,
		"connected_at":       c.ConnectedAt,
	}).Error()
}
