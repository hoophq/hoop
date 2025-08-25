package authinterceptor

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GatewayContextKey struct{}
type GatewayContext struct {
	UserContext models.Context
	Connection  types.ConnectionInfo
	Agent       models.Agent
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
	case *models.Agent:
		if _, ok := into.(*models.Agent); ok {
			*into.(*models.Agent) = *v
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
