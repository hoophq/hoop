package clientstate

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgproxymanager "github.com/hoophq/hoop/gateway/pgrest/proxymanager"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type option struct {
	key string
	val string
}

func DeterministicClientUUID(userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("clientstate/"+userID)).String()
}

// TODO: move to pgproxymanger package
// Update creates or updates a new entity with the given status based in the user uid.
func Update(ctx pgrest.UserContext, status types.ClientStatusType, opts ...*option) (*types.Client, error) {
	docuuid := DeterministicClientUUID(ctx.GetUserID())
	obj, err := pgproxymanager.New().FetchOne(ctx, docuuid)
	if err != nil {
		return nil, fmt.Errorf("failed fetching auto connect entity, err=%v", err)
	}

	// status ready must reset attributes
	if obj == nil || status == types.ClientStatusReady {
		obj = &types.Client{
			ID:          docuuid,
			OrgID:       ctx.GetOrgID(),
			ConnectedAt: time.Now().UTC(),
		}
	}

	obj.Status = status
	if len(opts) > 0 {
		if len(obj.ClientMetadata) == 0 {
			obj.ClientMetadata = map[string]string{}
		}
		for _, opt := range opts {
			switch opt.key {
			case "connection":
				obj.RequestConnectionName = opt.val
			case "port":
				obj.RequestPort = opt.val
			case "access-duration":
				d, _ := time.ParseDuration(opt.val)
				obj.RequestAccessDuration = d
			default:
				obj.ClientMetadata[opt.key] = opt.val
			}
		}
	}

	if status == types.ClientStatusConnected {
		if obj.RequestConnectionName == "" || obj.RequestPort == "" {
			return nil, fmt.Errorf("connection and port attributes are required for connected state")
		}
	}

	return obj, pgproxymanager.New().Update(ctx, obj)
}

func WithOption(k, v string) *option { return &option{key: k, val: v} }
func WithRequestAttributes(connectionName, port, accessDuration string) []*option {
	return []*option{
		{key: "connection", val: connectionName},
		{key: "port", val: port},
		{key: "access-duration", val: accessDuration},
	}
}
