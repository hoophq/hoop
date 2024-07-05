package streamclient

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// InitProxyMemoryCleanup ensures the cleanup of proxy streams in memory
func InitProxyMemoryCleanup() {
	go func() {
		for {
			items := proxyStore.List()
			log.Debugf("executing proxy memory cleanup process, total=%v", len(items))
			for sid, obj := range proxyStore.List() {
				s, _ := obj.(*ProxyStream)
				if s == nil {
					log.With("sid", sid).Warnf("removing empty proxy object")
					proxyStore.Del(sid)
					continue
				}
				timeout := s.stateTime.Add(proxyMaxTimeoutDuration)
				if time.Now().After(timeout) {
					errMsg := fmt.Errorf("reached max timeout (%s) waiting for session to end", proxyMaxTimeoutDuration.String())
					log.With("sid", sid).Info(errMsg)
					s.Close(errMsg)
				}
			}
			time.Sleep(time.Minute * 15)
		}
	}()
}

var (
	proxyMaxTimeoutDuration = time.Hour * 48
	proxyStore              = memory.New()
)

type ProxyStream struct {
	pb.Transport_ConnectServer

	// TODO: change to client context
	context        context.Context
	cancelFn       context.CancelCauseFunc
	metadata       metadata.MD // TODO: remove this from memory?
	runtimePlugins []runtimePlugin
	pluginCtx      *plugintypes.Context
	stateTime      time.Time
}

func GetProxyStream(sid string) *ProxyStream {
	obj := proxyStore.Get(sid)
	stream, _ := obj.(*ProxyStream)
	return stream
}

func NewProxy(pluginCtx *plugintypes.Context, s pb.Transport_ConnectServer) *ProxyStream {
	streamCtx := s.Context()
	ctx, cancelFn := context.WithCancelCause(streamCtx)
	md, _ := metadata.FromIncomingContext(streamCtx)
	if md == nil {
		md = metadata.MD{}
	}

	stream := &ProxyStream{
		Transport_ConnectServer: s,
		pluginCtx:               pluginCtx,
		context:                 ctx,
		cancelFn:                cancelFn,
		metadata:                md,
		stateTime:               time.Now().UTC(),
	}
	// The api layer or any public client could propagate a session id.
	// We could not rely on clients sending this information.
	// An uuid could be sent and override an existent session
	sessionID := stream.GetMeta("session-id")
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	stream.pluginCtx.SID = sessionID
	stream.pluginCtx.ClientOrigin = stream.GetMeta("origin")
	stream.pluginCtx.ClientVerb = stream.GetMeta("verb")
	return stream
}

// Override context from transport stream
func (s *ProxyStream) Context() context.Context { return s.context }
func (s *ProxyStream) ContextCauseError() error { return context.Cause(s.context) }
func (s *ProxyStream) GetMeta(key string) string {
	v := s.metadata.Get(key)
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

// SetPluginContext allows overriding the plugin context configuration
func (s *ProxyStream) SetPluginContext(fn func(pctx *plugintypes.Context)) { fn(s.pluginCtx) }
func (s *ProxyStream) PluginContext() plugintypes.Context                  { return *s.pluginCtx }

func (s *ProxyStream) String() string {
	return fmt.Sprintf("user=%v,hostname=%v,origin=%v,verb=%v,platform=%v,version=%v,license=%v",
		s.pluginCtx.UserEmail,
		s.GetMeta("hostname"),
		s.GetMeta("origin"),
		s.GetMeta("verb"),
		s.GetMeta("platform"),
		s.GetMeta("version"),
		s.pluginCtx.OrgLicenseType,
	)
}
func (s *ProxyStream) Save() (err error) {
	if err := s.pluginCtx.Validate(); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// the client-api has additional logic when managing session
	// this behavior should be removed and api layer must act a read only
	// client when interacting with sessions
	sessionScript := ""
	sessionLabels := map[string]string{}
	var sessionMetadata map[string]any
	if s.pluginCtx.ClientOrigin == pb.ConnectionOriginClientAPI ||
		s.pluginCtx.ClientOrigin == pb.ConnectionOriginClientAPIRunbooks {
		// TODO: refactor to use pgrest functions
		session, err := sessionstorage.FindOne(s.pluginCtx, s.pluginCtx.SID)
		if err != nil {
			return status.Errorf(codes.Internal, "fail obtaining existent session")
		}
		if session != nil {
			sessionScript = session.Script["data"]
			sessionLabels = session.Labels
			sessionMetadata = session.Metadata
		}
	}
	s.pluginCtx.Script = sessionScript
	s.pluginCtx.Labels = sessionLabels
	s.pluginCtx.Metadata = sessionMetadata
	s.runtimePlugins, err = loadRuntimePlugins(*s.pluginCtx)
	if err != nil {
		return
	}
	// set stream to memory
	proxyStore.Set(s.pluginCtx.SID, s)
	return
}

func (s *ProxyStream) Close(errMsg error) error {
	// prevent calling if the stream is not in the store
	if !proxyStore.Has(s.pluginCtx.SID) {
		return nil
	}
	_ = s.SendToAgent(&pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(s.pluginCtx.SID),
		},
	})
	_ = s.PluginExecOnDisconnect(*s.pluginCtx, errMsg)
	s.cancelFn(errMsg)
	proxyStore.Del(s.pluginCtx.SID)
	return nil
}

func (s *ProxyStream) SendToAgent(pkt *pb.Packet) error {
	if agentStream := GetAgentStream(s.StreamAgentID()); agentStream != nil {
		return agentStream.Send(pkt)
	}
	return pb.ErrAgentOffline
}

func (s *ProxyStream) IsAgentOnline() bool { return IsAgentOnline(s.StreamAgentID()) }

// If the agent is a multi connection type, it returns a deterministic uuid
// based on the agent id and the id of the connection, otherwise it returns the
// agent_id of the connection
func (s *ProxyStream) StreamAgentID() streamtypes.ID {
	if s.pluginCtx.AgentMode == pb.AgentModeMultiConnectionType {
		return streamtypes.NewStreamID(s.pluginCtx.AgentID, s.pluginCtx.ConnectionName)
	}
	return streamtypes.NewStreamID(s.pluginCtx.AgentID, "")
}

func DisconnectAllProxies(reason error) chan struct{} {
	proxies := proxyStore.List()
	log.Infof("disconnecting all proxies=%v, reason=%v", len(proxies), reason)
	donec := make(chan struct{})
	go func() {
		for _, obj := range proxies {
			if s, _ := obj.(*ProxyStream); s != nil {
				s.Close(reason)
			}
		}
		// give 5 seconds to wait for goroutines to finish
		// in the future it could be better to block when calling the
		// underline methods
		time.Sleep(time.Second * 5)
		close(donec)
	}()
	return donec
}

func disconnectProxiesByAgent(pctx plugintypes.Context, errMsg error) {
	for _, obj := range proxyStore.List() {
		s, _ := obj.(*ProxyStream)
		// make sure to send disconnect to both clients
		if s != nil && s.pluginCtx.AgentID == pctx.AgentID {
			_ = s.PluginExecOnDisconnect(pctx, errMsg)
			_ = s.Close(errMsg)
		}
	}
}
