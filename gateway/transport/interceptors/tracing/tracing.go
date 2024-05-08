package tracinginterceptor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var tracingConnectionStore = memory.New()

// serverStreamWrapper could override methods from the grpc.StreamServer interface.
// using this wrapper it's possible to intercept calls from a grpc server
type serverStreamWrapper struct {
	grpc.ServerStream

	startTime  time.Time
	totalBytes int64
}

type interceptor struct {
	apiURL string

	shutdownFn monitoring.ShutdownFn
}

func New(apiURL string) grpc.StreamServerInterceptor {
	// TODO: call this func when the gateway shutdowns
	shutdownFn, err := monitoring.NewOpenTracing(apiURL, "f26akXhvu7OG1PKqTVoUZB")
	if err != nil {
		log.Errorf("failed initializing open tracing client, err=%v", err)
	}
	return (&interceptor{apiURL: apiURL, shutdownFn: shutdownFn}).StreamServerInterceptor
}

func (s *serverStreamWrapper) SendMsg(m any) error {
	pkt, ok := m.(*pb.Packet)
	if !ok {
		return s.ServerStream.SendMsg(m)
	}
	s.totalBytes += int64(len(pkt.Payload))
	if pkt.Spec == nil {
		return s.ServerStream.SendMsg(m)
	}
	switch pkt.Type {
	case pbagent.SessionOpen, pbclient.SessionOpenOK:
		if v := tracingConnectionStore.Get(string(pkt.Spec[pb.SpecGatewaySessionID])); v != nil {
			_, span := otel.Tracer("gateway").Start(v.(context.Context), pkt.Type)
			if pkt.Type == pbagent.SessionOpen {
				_, hasClientArgs := pkt.Spec[pb.SpecClientExecArgsKey]
				_, hasClientEnvVars := pkt.Spec[pb.SpecClientExecEnvVar]
				span.SetAttributes(attribute.Bool("hoop.gateway.has-client-args", hasClientArgs))
				span.SetAttributes(attribute.Bool("hoop.gateway.has-client-envvars", hasClientEnvVars))
			}
			span.End()
		}
	case pbclient.SessionClose:
		if v := tracingConnectionStore.Get(string(pkt.Spec[pb.SpecGatewaySessionID])); v != nil {
			_, span := otel.Tracer("gateway").Start(v.(context.Context), pkt.Type)
			span.SetAttributes(attribute.Int64("hoop.gateway.agent-sent-bytes", s.totalBytes))
			closeErr := "<nil>"
			if len(pkt.Payload) > 0 {
				closeErr = string(pkt.Payload)
				if len(pkt.Payload) > 250 {
					closeErr = string(pkt.Payload[0:250]) + " ..."
				}
			}
			span.SetAttributes(attribute.String("hoop.gateway.session-close-err", closeErr))
			span.End()
		}
	}
	return s.ServerStream.SendMsg(m)
}

func (i *interceptor) StreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handlerFn grpc.StreamHandler) error {
	md, _ := metadata.FromIncomingContext(ss.Context())
	tracer := otel.Tracer("gateway")
	clientOrigin := commongrpc.MetaGet(md, "origin")
	if clientOrigin == pb.ConnectionOriginAgent {
		spanCtx, err := newBaggageMembers(context.Background(), map[string]string{
			"hoop.gateway.environment": monitoring.NormalizeEnvironment(os.Getenv("API_URL")),
			"hoop.gateway.org-id":      commongrpc.MetaGet(md, "org-id"),
			"hoop.gateway.agent-name":  commongrpc.MetaGet(md, "agent-name"),
			"hoop.gateway.agent-mode":  commongrpc.MetaGet(md, "agent-mode"),
			"hoop.gateway.hostname":    commongrpc.MetaGet(md, "hostname"),
			"hoop.gateway.platform":    commongrpc.MetaGet(md, "platform"),
			"hoop.gateway.version":     commongrpc.MetaGet(md, "version"),
			"hoop.gateway.origin":      clientOrigin,
		})
		if err != nil {
			log.Error(err)
			return handlerFn(srv, ss)
		}
		ctx, span := tracer.Start(spanCtx, "GrpcAgentConnect")
		span.End()
		ssw := &serverStreamWrapper{ss, time.Now().UTC(), 0}
		streamErr := handlerFn(srv, ssw)
		_, span = tracer.Start(ctx, "GrpcAgentClose")
		span.SetAttributes(attribute.Float64("hoop.gateway.client-duration", time.Now().UTC().Sub(ssw.startTime).Seconds()))
		span.SetAttributes(attribute.String("hoop.gateway.disconnect-err", fmt.Sprintf("%v", streamErr)))
		span.End()
		return streamErr
	}

	sessionID := commongrpc.MetaGet(md, "session-id")
	spanCtx, err := newBaggageMembers(context.Background(), map[string]string{
		"hoop.gateway.environment":           monitoring.NormalizeEnvironment(os.Getenv("API_URL")),
		"hoop.gateway.org-id":                commongrpc.MetaGet(md, "org-id"),
		"hoop.gateway.sid":                   sessionID,
		"hoop.gateway.user-email":            commongrpc.MetaGet(md, "user-email"),
		"hoop.gateway.connection":            commongrpc.MetaGet(md, "connection-name"),
		"hoop.gateway.connection-type":       commongrpc.MetaGet(md, "connection-type"),
		"hoop.gateway.connection-agent":      commongrpc.MetaGet(md, "connection-agent"),
		"hoop.gateway.connection-agent-mode": commongrpc.MetaGet(md, "connection-agent-mode"),
		"hoop.gateway.hostname":              commongrpc.MetaGet(md, "hostname"),
		"hoop.gateway.platform":              commongrpc.MetaGet(md, "platform"),
		"hoop.gateway.version":               commongrpc.MetaGet(md, "version"),
		"hoop.gateway.origin":                clientOrigin,
		"hoop.gateway.verb":                  commongrpc.MetaGet(md, "verb"),
	})
	if err != nil {
		log.Error(err)
		return handlerFn(srv, ss)
	}
	ctx, span := tracer.Start(spanCtx, "GrpcClientConnect")
	span.SetAttributes(attribute.String("hoop.gateway.ua", commongrpc.MetaGet(md, "user-agent")))
	span.End()

	if sessionID != "" {
		tracingConnectionStore.Set(sessionID, ctx)
		defer tracingConnectionStore.Del(sessionID)
	}
	ssw := &serverStreamWrapper{ss, time.Now().UTC(), 0}
	streamErr := handlerFn(srv, ssw)
	_, span = tracer.Start(ctx, "GrpcClientClose")
	span.SetAttributes(attribute.Float64("hoop.gateway.client-duration", time.Now().UTC().Sub(ssw.startTime).Seconds()))
	var disconnectErr error
	if streamErr != nil && len(streamErr.Error()) > 250 {
		disconnectErr = fmt.Errorf(streamErr.Error()[0:250])
	}
	span.SetAttributes(attribute.String("hoop.gateway.disconnect-err", fmt.Sprintf("%v", disconnectErr)))
	span.End()
	return streamErr
}

func newBaggageMembers(ctx context.Context, attrs map[string]string) (context.Context, error) {
	bag := baggage.FromContext(ctx)
	var errors []string
	for key, val := range attrs {
		val = strings.ReplaceAll(val, " ", "_")
		member, err := baggage.NewMember(key, val)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed creating new member for %q, %v", key, err))
			continue
		}
		bag, err = bag.SetMember(member)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed setting new member for %q, %v", key, err))
			continue
		}
	}
	if len(errors) > 0 {
		return nil, fmt.Errorf("baggage errors: %v", errors)
	}
	return baggage.ContextWithBaggage(ctx, bag), nil
}
