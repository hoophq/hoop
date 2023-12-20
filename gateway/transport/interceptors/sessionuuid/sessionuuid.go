package sessionuuidinterceptor

import (
	"context"

	"github.com/google/uuid"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type interceptor struct{}

// serverStreamWrapper could override methods from the grpc.StreamServer interface.
// using this wrapper it's possible to intercept calls from a grpc server
type serverStreamWrapper struct {
	grpc.ServerStream
}

func New() grpc.StreamServerInterceptor { return (&interceptor{}).StreamServerInterceptor }

func (s *serverStreamWrapper) Context() context.Context {
	ctx := s.ServerStream.Context()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// skip it, a client already provided a session-id
		// or it's an agent client
		if len(md.Get("session-id")) > 0 || commongrpc.MetaGet(md, "origin") == proto.ConnectionOriginAgent {
			return ctx
		}

		newMD := md.Copy()
		newMD.Append("session-id", uuid.NewString())
		return metadata.NewIncomingContext(ctx, newMD)
	}
	log.Error("failed obtaining metadata from incoming context")
	return nil
}

func (i *interceptor) StreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, &serverStreamWrapper{ss})
}
