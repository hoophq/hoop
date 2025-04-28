package streamclient

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/transport/connectionstatus"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	agentStore               = memory.New()
	defaultAgentMetadataKeys = []string{
		"hostname",
		"platform",
		"machine-id",
		"kernel-version",
		"version",
		"go-version",
		"compiler",
	}
)

type AgentStream struct {
	pb.Transport_ConnectServer
	context        context.Context
	cancelFn       context.CancelCauseFunc
	connectionName string
	agent          pgrest.Agent
	metadata       metadata.MD
}

func GetAgentStream(streamAgentID streamtypes.ID) *AgentStream {
	obj := agentStore.Get(streamAgentID.String())
	stream, _ := obj.(*AgentStream)
	return stream
}

func IsAgentOnline(streamAgentID streamtypes.ID) bool { return GetAgentStream(streamAgentID) != nil }
func NewAgent(a pgrest.Agent, s pb.Transport_ConnectServer) *AgentStream {
	streamCtx := s.Context()
	ctx, cancelFn := context.WithCancelCause(streamCtx)
	md, _ := metadata.FromIncomingContext(streamCtx)
	if md == nil {
		md = metadata.MD{}
	}
	stream := &AgentStream{
		Transport_ConnectServer: s,
		context:                 ctx,
		cancelFn:                cancelFn,
		agent:                   a,
		metadata:                md,
	}
	stream.connectionName = stream.GetMeta("connection-name")
	return stream
}

// Override context from transport stream
func (s *AgentStream) Context() context.Context { return s.context }
func (s *AgentStream) GetMeta(key string) string {
	v := s.metadata.Get(key)
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

func (s *AgentStream) validate() error {
	if s.agent.OrgID == "" || s.agent.ID == "" || s.agent.Name == "" {
		return status.Error(codes.FailedPrecondition, "missing required agent attributes")
	}
	if s.agent.Mode == pb.AgentModeMultiConnectionType {
		if s.connectionName == "" {
			return status.Error(codes.FailedPrecondition, "missing connection-name attribute")
		}
		conn, err := models.GetConnectionByNameOrID(s.GetOrgID(), s.connectionName)
		if err != nil || conn == nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed validating connection, reason=%v", err))
		}
		if s.agent.ID != conn.AgentID.String || conn.ManagedBy.String == "" {
			return status.Error(codes.InvalidArgument,
				fmt.Sprintf("connection %v is not managed by agent %v", s.connectionName, s.agent.Name))
		}
	}
	return nil
}

func (s *AgentStream) StreamAgentID() streamtypes.ID {
	return streamtypes.NewStreamID(s.AgentID(), s.connectionName)
}

func (s *AgentStream) GetOrgID() string       { return s.agent.OrgID }
func (s *AgentStream) GetOrgName() string     { return s.agent.Org.Name }
func (s *AgentStream) AgentID() string        { return s.agent.ID }
func (s *AgentStream) AgentName() string      { return s.agent.Name }
func (s *AgentStream) AgentVersion() string   { return s.agent.GetMeta("version") }
func (s *AgentStream) ConnectionName() string { return s.connectionName }
func (s *AgentStream) String() string         { return s.agent.String() }
func (s *AgentStream) Save() (err error) {
	if err = s.validate(); err != nil {
		return
	}
	streamAgentID := s.StreamAgentID().String()
	if existentStream := agentStore.Get(streamAgentID); existentStream != nil {
		return status.Error(codes.FailedPrecondition, "agent already connected")
	}
	agentStore.Set(streamAgentID, s)
	defer func() {
		if err != nil {
			err = status.Error(codes.Internal, err.Error())
			agentStore.Del(streamAgentID)
		}
	}()

	return connectionstatus.SetOnline(s, s.StreamAgentID(), s.parseDefaultMetadata())
}

func (s *AgentStream) Close(pctx plugintypes.Context, errMsg error) error {
	// prevent calling this method if the stream is removed from the store
	streamAgentID := s.StreamAgentID().String()
	if !agentStore.Has(streamAgentID) {
		return nil
	}
	agentStore.Del(streamAgentID)
	_ = connectionstatus.SetOffline(s, s.StreamAgentID(), s.parseDefaultMetadata())
	disconnectProxiesByAgent(pctx, errMsg)
	return nil
}

func (s *AgentStream) parseDefaultMetadata() map[string]string {
	metadata := map[string]string{}
	for _, key := range defaultAgentMetadataKeys {
		metadata[key] = s.GetMeta(key)
	}
	return metadata
}
