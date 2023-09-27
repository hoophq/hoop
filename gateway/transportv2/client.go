package transportv2

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/analytics"
	apiconnectionapps "github.com/runopsio/hoop/gateway/api/connectionapps"
	"github.com/runopsio/hoop/gateway/apiclient"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/transportv2/auditfs"
	"github.com/runopsio/hoop/gateway/transportv2/memorystreams"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func SubscribeClient(ctx *ClientContext, stream pb.Transport_ConnectServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	if err := ctx.ValidateConnectionAttrs(); err != nil {
		return err
	}
	ctx.verb = mdget(md, "verb")
	clientOrigin := mdget(md, "origin")
	hostname := mdget(md, "hostname")
	// TODO: add embedded flow mode
	conn := ctx.Connection

	switch string(ctx.Connection.Type) {
	case pb.ConnectionTypeCommandLine: // noop - this type can connect/exec
	case pb.ConnectionTypeTCP:
		if ctx.verb == pb.ClientVerbExec {
			return status.Errorf(codes.InvalidArgument,
				fmt.Sprintf("exec is not allowed for tcp type connections. Use 'hoop connect %s' instead", conn.Name))
		}
	}

	if conn.AgentMode == pb.AgentModeEmbeddedType {
		if !memorystreams.HasAgent(conn.AgentID) {
			log.With("user", ctx.UserContext.UserEmail, "agent-id", conn.AgentID).
				Infof("requesting connection with remote agent")
			if err := apiconnectionapps.RequestGrpcConnection(conn.AgentID, memorystreams.HasAgent); err != nil {
				log.Warnf("%v %v", err, conn.AgentID)
				return status.Errorf(codes.Aborted, err.Error())
			}
			log.With("conn", conn.Name, "agent-id", conn.AgentID).
				Infof("agent established connection with success connection established")
		}
	}

	ctx.sessionID = mdget(md, "session-id")
	if ctx.sessionID == "" {
		client := apiclient.New(ctx.UserContext.ApiURL, ctx.BearerToken)
		ctx.sessionID, _ = client.OpenSession()
	}
	// subscribe the stream into the memory
	memorystreams.SetClient(ctx.sessionID, stream)
	startDisconnectClientSink(ctx.sessionID, clientOrigin, func(err error) {
		// remove session from memory store

		defer memorystreams.DelClient(ctx.sessionID)
		// defer clientStore.Del(ctx.sessionID)
		if s := memorystreams.GetClient(conn.AgentID); s != nil {
			_ = stream.Send(&pb.Packet{
				Type: pbagent.SessionClose,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: []byte(ctx.sessionID),
				},
			})
		}
		_ = auditfs.Close(ctx.sessionID, apiclient.New(ctx.UserContext.ApiURL, ctx.BearerToken))
	})

	eventName := analytics.EventGrpcExec
	if ctx.verb == pb.ClientVerbConnect {
		eventName = analytics.EventGrpcConnect
	}
	analytics.New().Track(&ctx.UserContext, eventName, map[string]any{
		"connection-name": conn.Name,
		"connection-type": conn.Type,
		"client-version":  mdget(md, "version"),
		"go-version":      mdget(md, "go-version"),
		"platform":        mdget(md, "platform"),
		"hostname":        hostname,
		"user-agent":      mdget(md, "user-agent"),
		"origin":          clientOrigin,
		"verb":            ctx.verb,
	})
	log.With("sid", ctx.sessionID, "mode", conn.AgentMode, "agent-name", conn.AgentName).
		Infof("proxy connected: user=%v,hostname=%v,origin=%v,verb=%v,platform=%v,version=%v,goversion=%v",
			ctx.UserContext.UserEmail, hostname, clientOrigin, ctx.verb,
			mdget(md, "platform"), mdget(md, "version"), mdget(md, "goversion"))

	auditOpts := auditfs.Options{
		OrgID:          ctx.UserContext.OrgID,
		SessionID:      ctx.sessionID,
		ConnectionType: conn.Type,
		ConnectionName: conn.Name,
		// TODO: must came from the api!
		StartDate: time.Now().UTC(),
	}
	if err := auditfs.Open(auditOpts); err != nil {
		log.Errorf("failed auditing session, err=%v", err)
		return status.Error(codes.Internal, "internal error, failed auditing session")
	}

	clientErr := listenClientMessages(ctx, stream)
	if status, ok := status.FromError(clientErr); ok && status.Code() == codes.Canceled {
		log.With("sid", ctx.sessionID, "origin", clientOrigin, "mode", conn.AgentMode).Infof("grpc client connection canceled")
	}
	defer disconnectClient(ctx.sessionID, clientErr)
	if clientErr != nil {
		return clientErr
	}
	return clientErr
}

func listenClientMessages(ctx *ClientContext, stream pb.Transport_ConnectServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		default:
		}
		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.With("sid", ctx.sessionID).Debugf("EOF")
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from client, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}
		if pkt.Type == pbgateway.KeepAlive {
			continue
		}
		// audit session packets
		if err := auditfs.Write(ctx.sessionID, pkt); err != nil {
			log.Errorf("failed auditing packet, err=%v", err)
			return status.Error(codes.Internal, "internal error, failed auditing packet")
		}
		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}
		pkt.Spec[pb.SpecGatewaySessionID] = []byte(ctx.sessionID)
		log.With("sid", ctx.sessionID).Debugf("receive client packet type [%s]", pkt.Type)
		switch pb.PacketType(pkt.Type) {
		case pbagent.SessionOpen:
			err = processSessionOpenPacket(ctx, pkt)
			if err != nil {
				return err
			}
		default:
			agentStream := memorystreams.GetAgent(ctx.Connection.AgentID)
			if agentStream == nil {
				return fmt.Errorf("agent not found for connection %s", ctx.Connection.Name)
			}
			_ = agentStream.Send(pkt)
		}
	}
}

func processSessionOpenPacket(ctx *ClientContext, pkt *pb.Packet) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID: []byte(ctx.sessionID),
		pb.SpecConnectionType:   []byte(ctx.Connection.Type),
	}

	gcpDLPRawCredentials := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if gcpDLPRawCredentials != "" {
		spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(gcpDLPRawCredentials)
	}

	agentStream := memorystreams.GetAgent(ctx.Connection.AgentID)
	if agentStream == nil {
		log.With("user", ctx.UserContext.UserEmail, "id", ctx.Connection.AgentID, "name", ctx.Connection.AgentName).
			Warn(pb.ErrAgentOffline)
		spec[pb.SpecClientExecArgsKey] = pkt.Spec[pb.SpecClientExecArgsKey]
		if clientStream := memorystreams.GetClient(ctx.sessionID); clientStream != nil {
			_ = clientStream.Send(&pb.Packet{
				Type: pbclient.SessionOpenAgentOffline,
				Spec: spec,
			})
		}
		return pb.ErrAgentOffline
	}
	var clientArgs []string
	if pkt.Spec != nil {
		encArgs := pkt.Spec[pb.SpecClientExecArgsKey]
		if len(encArgs) > 0 {
			if err := pb.GobDecodeInto(encArgs, &clientArgs); err != nil {
				log.Errorf("failed decoding client arguments, error=%v", err)
				return status.Error(codes.Internal, "failed decoding client arguments")
			}
		}
	}

	var infoTypes []string
	for _, p := range ctx.Connection.Policies {
		if p.Type == apitypes.PolicyDataMaskingType {
			infoTypes = p.Config
			break
		}
	}

	connParams, err := pb.GobEncode(&pb.AgentConnectionParams{
		ConnectionName: ctx.Connection.Name,
		ConnectionType: ctx.Connection.Type,
		UserID:         ctx.UserContext.UserID,
		EnvVars:        ctx.Connection.Secrets,
		CmdList:        ctx.Connection.CmdEntrypoint,
		ClientArgs:     clientArgs,
		ClientVerb:     ctx.verb,
		DLPInfoTypes:   infoTypes,
		// TODO: add hook list (secretsmanager)
		PluginHookList: nil,
	})
	if err != nil {
		log.Errorf("failed encoding connection parameters, err=%v", err)
		return status.Error(codes.Internal, "failed encoding connection parameters")
	}
	spec[pb.SpecAgentConnectionParamsKey] = connParams
	// Propagate client spec.
	// Do not allow replacing system ones
	for key, val := range pkt.Spec {
		if _, ok := spec[key]; ok {
			continue
		}
		spec[key] = val
	}
	_ = agentStream.Send(&pb.Packet{Type: pbagent.SessionOpen, Spec: spec})
	return nil
}

// startDisconnectClientSink listen for disconnects when the disconnect channel is closed
// it timeout after 48 hours closing the client.
func startDisconnectClientSink(clientID, clientOrigin string, disconnectFn func(err error)) {
	// disconnectSink.mu.Lock()
	// defer disconnectSink.mu.Unlock()
	// disconnectCh := make(chan error)
	// disconnectSink.items[clientID] = disconnectCh
	// log.With("id", clientID).Debugf("start disconnect sink for %v", clientOrigin)
	// go func() {
	// 	switch clientOrigin {
	// 	case pb.ConnectionOriginAgent:
	// 		err := <-disconnectCh
	// 		// wait to get time to persist any resources performed async
	// 		defer closeChWithSleep(disconnectCh, time.Millisecond*150)
	// 		log.With("id", clientID).Infof("disconnecting agent client, reason=%v", err)
	// 		disconnectFn(err)
	// 	default:
	// 		// wait to get time to persist any resources performed async
	// 		defer closeChWithSleep(disconnectCh, time.Millisecond*150)
	// 		select {
	// 		case err := <-disconnectCh:
	// 			log.With("id", clientID).Infof("disconnecting proxy client, reason=%v", err)
	// 			disconnectFn(err)
	// 		case <-time.After(time.Hour * 48):
	// 			log.With("id", clientID).Warnf("timeout (48h), disconnecting proxy client")
	// 			disconnectFn(fmt.Errorf("timeout (48h)"))
	// 		}
	// 	}
	// }()
}

// disconnectClient closes the disconnect sink channel
// triggering the disconnect logic at startDisconnectClientSink
func disconnectClient(uid string, err error) {
	// disconnectSink.mu.Lock()
	// defer disconnectSink.mu.Unlock()
	// disconnectCh, ok := disconnectSink.items[uid]
	// if !ok {
	// 	return
	// }
	// if err != nil {
	// 	select {
	// 	case disconnectCh <- err:
	// 	case <-time.After(time.Millisecond * 100):
	// 		log.With("uid", uid).Errorf("timeout (100ms) send disconnect error to sink")
	// 	}
	// }
	// delete(disconnectSink.items, uid)
}
