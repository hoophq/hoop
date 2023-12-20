package serviceaccountstorage

import (
	"fmt"

	"github.com/google/uuid"
	pgserviceaccounts "github.com/runopsio/hoop/gateway/pgrest/serviceaccounts"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
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
	return pgserviceaccounts.New().FetchOne(ctx, xtID)
}

func UpdateServiceAccount(ctx *storagev2.Context, svcAccount *types.ServiceAccount) error {
	_, err := pgserviceaccounts.New().Upsert(ctx, svcAccount)
	return err
}

func List(ctx *storagev2.Context) ([]types.ServiceAccount, error) {
	return pgserviceaccounts.New().FetchAll(ctx)
}

// DeterministicXtID generates a deterministic xtid based on the value of subject
func DeterministicXtID(subject string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("serviceaccount/%s", subject))).String()
}
