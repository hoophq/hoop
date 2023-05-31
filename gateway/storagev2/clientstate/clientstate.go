package clientstate

import (
	"bytes"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type option struct {
	key string
	val string
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
	xtuid, err := uuid.NewRandomFromReader(bytes.NewBufferString(ctx.UserID))
	if err != nil {
		return nil, fmt.Errorf("failed generating auto connect id, err=%v", err)
	}
	obj, err := GetEntity(ctx, xtuid.String())
	if err != nil {
		return nil, fmt.Errorf("failed fetching auto connect entity, err=%v", err)
	}

	// status ready must reset attributes
	if obj == nil || status == types.ClientStatusReady {
		obj = &types.Client{
			ID:    xtuid.String(),
			OrgID: ctx.OrgID,
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
func WithRequestAttributes(connectionName, port string) []*option {
	return []*option{
		{key: "connection", val: connectionName},
		{key: "port", val: port},
	}
}
