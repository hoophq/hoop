package authinterceptor

import (
	"context"
	"fmt"

	"github.com/runopsio/hoop/gateway/agent"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GatewayContextKey struct{}
type GatewayContext struct {
	UserContext types.APIContext
	Connection  types.ConnectionInfo
	Agent       apitypes.Agent

	BearerToken string
	IsAdminExec bool
}

func (c *GatewayContext) ValidateConnectionAttrs() error {
	if c.Connection.Name == "" || c.Connection.AgentID == "" ||
		c.Connection.ID == "" || c.Connection.Type == "" {
		return status.Error(codes.InvalidArgument, "missing required connection attributes")
	}
	return nil
}

func GetGatewayContext(ctx context.Context) (any, error) {
	if ctx == nil {
		return nil, status.Error(codes.Internal, "authentication context not found (nil)")
	}
	val := ctx.Value(GatewayContextKey{})
	if val == nil {
		return nil, status.Error(codes.Internal, "authentication context not found")
	}
	return val, nil
}

func ParseGatewayContextInto(ctx context.Context, into any) error {
	val, err := GetGatewayContext(ctx)
	if err != nil {
		return err
	}
	var assigned bool
	switch v := val.(type) {
	case *GatewayContext:
		if _, ok := into.(*GatewayContext); ok {
			*into.(*GatewayContext) = *v
			assigned = true
		}
	case *types.ClientKey:
		if _, ok := into.(*types.ClientKey); ok {
			*into.(*types.ClientKey) = *v
			assigned = true
		}
	case *apitypes.Agent:
		if _, ok := into.(*agent.Agent); ok {
			*into.(*apitypes.Agent) = *v
			assigned = true
		}
	default:
		return status.Error(codes.Unauthenticated,
			fmt.Sprintf("invalid authentication, missing auth context, type: %T", val))
	}
	if !assigned {
		return status.Error(codes.Internal,
			fmt.Sprintf("invalid authentication, failed assigning context %T to %T", val, into))
	}
	return nil
}
