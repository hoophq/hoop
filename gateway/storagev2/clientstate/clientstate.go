package clientstate

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type option struct {
	key string
	val string
}

func DeterministicClientUUID(userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("clientstate/"+userID)).String()
}

func GetEntity(ctx *storagev2.Context, xtID string) (*types.Client, error) {
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.Client
	return &obj, edn.Unmarshal(data, &obj)
}

// Update creates or updates a new entity with the given status based in the user uid.
func Update(ctx *storagev2.Context, status types.ClientStatusType, opts ...*option) (*types.Client, error) {
	if err := ctx.Validate(); err != nil {
		return nil, err
	}

	docuuid := DeterministicClientUUID(ctx.UserID)
	obj, err := GetEntity(ctx, docuuid)
	if err != nil {
		return nil, fmt.Errorf("failed fetching auto connect entity, err=%v", err)
	}

	// status ready must reset attributes
	if obj == nil || status == types.ClientStatusReady {
		obj = &types.Client{
			ID:          docuuid,
			OrgID:       ctx.OrgID,
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

	_, err = ctx.Put(obj)
	return obj, err
}

func WithOption(k, v string) *option { return &option{key: k, val: v} }
func WithRequestAttributes(connectionName, port, accessDuration string) []*option {
	return []*option{
		{key: "connection", val: connectionName},
		{key: "port", val: port},
		{key: "access-duration", val: accessDuration},
	}
}
