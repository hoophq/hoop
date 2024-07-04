package authinterceptor

import (
	"context"

	"github.com/hoophq/hoop/gateway/security/idp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func WithUnaryValidator(idpProvider *idp.Provider) grpc.ServerOption {
	i := &interceptor{idp: idpProvider}
	return grpc.UnaryInterceptor(i.UnaryValidator)
}

func (i *interceptor) UnaryValidator(ctx context.Context, srv any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "missing context metadata")
	}
	bearerToken, err := parseBearerToken(md)
	if err != nil {
		return nil, err
	}
	ag, err := i.authenticateAgent(bearerToken, md)
	if err != nil {
		return nil, err
	}
	newCtx := metadata.NewIncomingContext(
		context.WithValue(
			ctx,
			GatewayContextKey{},
			&GatewayContext{Agent: *ag},
		), md.Copy())
	return handler(newCtx, srv)

}
