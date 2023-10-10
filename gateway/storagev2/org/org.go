package orgstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func GetEntity(ctx *storagev2.Store, xtID string) (*types.Org, error) {
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.Org
	return &obj, edn.Unmarshal(data, &obj)
}

// ToggleApiV2 update an org to proxy requests to the api v2
func ToggleApiV2(ctx *storagev2.Store, orgID string) error {
	org, err := GetEntity(ctx, orgID)
	if err != nil {
		return fmt.Errorf("failed obtaining organization entity, err=%v", err)
	}
	if org == nil {
		return fmt.Errorf("organization not found")
	}
	if org.IsApiV2 {
		return fmt.Errorf("organization is already set to api v2")
	}
	_, err = ctx.Put(&types.Org{
		ID:      org.ID,
		IsApiV2: true,
		Name:    org.Name,
	})
	return err
}

// ToggleLegacyApi update an org to proxy requests to the legacy api
func ToggleLegacyApi(ctx *storagev2.Store, orgID string) error {
	org, err := GetEntity(ctx, orgID)
	if err != nil {
		return fmt.Errorf("failed obtaining organization entity, err=%v", err)
	}
	if org == nil {
		return fmt.Errorf("organization not found")
	}
	if !org.IsApiV2 {
		return fmt.Errorf("organization is already set to legacy")
	}
	_, err = ctx.Put(&types.Org{
		ID:      org.ID,
		IsApiV2: false,
		Name:    org.Name,
	})
	return err
}
