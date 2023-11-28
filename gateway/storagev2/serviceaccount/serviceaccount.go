package serviceaccountstorage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgserviceaccounts "github.com/runopsio/hoop/gateway/pgrest/serviceaccounts"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

// GetEntityWithOrgContext fetches a service account enforcing the organization id context
func GetEntityWithOrgContext(ctx *storagev2.Context, xtID string) (*types.ServiceAccount, error) {
	sa, err := GetEntity(ctx, xtID)
	if sa != nil && sa.OrgID != ctx.OrgID {
		return nil, err
	}
	return sa, err
}

// GetEntity returns active service account resources based on the xtid
func GetEntity(ctx *storagev2.Context, xtID string) (*types.ServiceAccount, error) {
	if pgrest.Rollout {
		return pgserviceaccounts.New().FetchOne(ctx, xtID)
	}
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.ServiceAccount
	if err := edn.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj.Status == types.ServiceAccountStatusActive {
		return &obj, nil
	}
	return nil, nil
}

func UpdateServiceAccount(ctx *storagev2.Context, svcAccount *types.ServiceAccount) error {
	if pgrest.Rollout {
		_, err := pgserviceaccounts.New().Upsert(ctx, svcAccount)
		return err
	}
	_, err := ctx.Put(svcAccount)
	return err
}

func List(ctx *storagev2.Context) ([]types.ServiceAccount, error) {
	if pgrest.Rollout {
		return pgserviceaccounts.New().FetchAll(ctx)
	}
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?c [*])] 
		:in [org]
		:where [[?c :serviceaccount/org org]
				[?c :serviceaccount/status "active"]]}
		:in-args [%q]}`, ctx.OrgID)
	b, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}

	var items [][]types.ServiceAccount
	if err := edn.Unmarshal(b, &items); err != nil {
		return nil, err
	}

	var itemList []types.ServiceAccount
	for _, item := range items {
		itemList = append(itemList, item[0])
	}
	return itemList, nil
}

// DeterministicXtID generates a deterministic xtid based on the value of subject
func DeterministicXtID(subject string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("serviceaccount/%s", subject))).String()
}
