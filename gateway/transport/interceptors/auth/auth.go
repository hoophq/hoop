package authinterceptor

import (
	"context"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/dsnkeys"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	pguserauth "github.com/runopsio/hoop/gateway/pgrest/userauth"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// serverStreamWrapper could override methods from the grpc.StreamServer interface.
// using this wrapper it's possible to intercept calls from a grpc server
type serverStreamWrapper struct {
	grpc.ServerStream

	newCtx    context.Context
	newCtxVal any
}

type interceptor struct {
	idp          *idp.Provider
	agentService *agent.Service
}

func New(idpProvider *idp.Provider, agentsvc *agent.Service) grpc.StreamServerInterceptor {
	return (&interceptor{
		idp:          idpProvider,
		agentService: agentsvc,
	}).StreamServerInterceptor
}

func (s *serverStreamWrapper) Context() context.Context {
	if s.newCtx != nil {
		return s.newCtx
	}
	ctx := s.ServerStream.Context()
	if s.newCtxVal == nil {
		return ctx
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// used to propagate information to interceptors running after this one.
		// this information could be used to identify the context of a grpc client
		mdCopy := md.Copy()
		switch v := s.newCtxVal.(type) {
		case *GatewayContext:
			if v.Connection.Type != "" {
				mdCopy.Set("connection-type", v.Connection.Type)
				mdCopy.Set("connection-agent", v.Connection.AgentName)
				mdCopy.Set("connection-agent-mode", v.Connection.AgentMode)
			}
			if v.UserContext.OrgID != "" {
				mdCopy.Set("org-id", v.UserContext.OrgID)
				mdCopy.Set("user-email", v.UserContext.UserEmail)
			}
			if v.Agent.ID != "" {
				mdCopy.Set("agent-name", v.Agent.Name)
				mdCopy.Set("agent-mode", v.Agent.Mode)
				mdCopy.Set("org-id", v.Agent.OrgID)
			}
		}
		s.newCtx = metadata.NewIncomingContext(
			context.WithValue(ctx, GatewayContextKey{}, s.newCtxVal), mdCopy)
		return s.newCtx
	}
	return ctx
}

func (i *interceptor) StreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	md, ok := metadata.FromIncomingContext(ss.Context())
	if !ok {
		return status.Error(codes.InvalidArgument, "missing context metadata")
	}
	clientOrigin := md.Get("origin")
	if len(clientOrigin) == 0 {
		md.Delete("authorization")
		log.Debugf("client missing origin, client-metadata=%v", md)
		return status.Error(codes.InvalidArgument, "missing client origin")
	}

	bearerToken, err := parseBearerToken(md)
	if err != nil {
		return err
	}

	var ctxVal any
	switch {
	case clientOrigin[0] == pb.ConnectionOriginAgent:
		// fallback to dsn agent authentication
		ag, err := i.authenticateAgent(bearerToken, md)
		if err != nil {
			return err
		}
		ctxVal = &GatewayContext{
			Agent:       *ag,
			BearerToken: bearerToken,
		}
	// client proxy manager authentication (access token)
	case clientOrigin[0] == pb.ConnectionOriginClientProxyManager:
		sub, err := i.idp.VerifyAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := pguserauth.New().FetchUserContext(sub)
		if userCtx.IsEmpty() {
			if err != nil {
				log.Error(err)
			}
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		ctxVal = &GatewayContext{
			UserContext: *userCtx.ToAPIContext(),
			BearerToken: bearerToken,
		}
	// client proxy authentication (access token)
	default:
		sub, err := i.idp.VerifyAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := pguserauth.New().FetchUserContext(sub)
		if userCtx.IsEmpty() {
			if err != nil {
				log.Error(err)
			}
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		gwctx := &GatewayContext{
			UserContext: *userCtx.ToAPIContext(),
			BearerToken: bearerToken,
		}
		gwctx.UserContext.ApiURL = i.idp.ApiURL
		connectionName := commongrpc.MetaGet(md, "connection-name")
		conn, err := i.getConnection(connectionName, userCtx)
		if err != nil {
			return err
		}
		if conn == nil {
			return status.Errorf(codes.NotFound, "connection not found")
		}
		gwctx.Connection = *conn
		ctxVal = gwctx
	}

	return handler(srv, &serverStreamWrapper{ss, nil, ctxVal})
}

func (i *interceptor) getConnection(name string, userCtx *pguserauth.Context) (*types.ConnectionInfo, error) {
	conn, err := apiconnections.FetchByName(userCtx, name)
	if err != nil {
		log.Errorf("failed retrieving connection %v, err=%v", name, err)
		sentry.CaptureException(err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain connection")
	}
	if conn == nil {
		return nil, nil
	}
	ag, err := i.agentService.FindByNameOrID(userCtx, conn.AgentId)
	if err != nil {
		log.Errorf("failed obtaining agent %v, err=%v", err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain agent from connection")
	}
	if ag == nil {
		return nil, status.Errorf(codes.NotFound, "agent not found")
	}
	return &types.ConnectionInfo{
		ID:            conn.ID,
		Name:          conn.Name,
		Type:          string(conn.Type),
		SubType:       conn.SubType,
		CmdEntrypoint: conn.Command,
		Secrets:       conn.Secrets,
		AgentID:       conn.AgentId,
		AgentMode:     ag.Mode,
		AgentName:     ag.Name,
	}, nil
}

func (i *interceptor) authenticateAgent(bearerToken string, md metadata.MD) (*apitypes.Agent, error) {
	if strings.HasPrefix(bearerToken, "x-agt-") {
		ag, err := i.agentService.FindByToken(bearerToken)
		if err != nil || ag == nil {
			md.Delete("authorization")
			log.Debugf("invalid agent authentication (legacy auth), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
			return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		return &apitypes.Agent{ID: ag.Id, OrgID: ag.OrgId, Name: ag.Name, Mode: ag.Mode,
			Metadata: apitypes.AgentAuthMetadata{
				Hostname:      commongrpc.MetaGet(md, "hostname"),
				Platform:      commongrpc.MetaGet(md, "platform"),
				MachineID:     commongrpc.MetaGet(md, "machine_id"),
				KernelVersion: commongrpc.MetaGet(md, "kernel_version"),
				Version:       commongrpc.MetaGet(md, "version"),
				GoVersion:     commongrpc.MetaGet(md, "go-version"),
				Compiler:      commongrpc.MetaGet(md, "compiler")}}, nil
	}
	dsn, err := dsnkeys.Parse(bearerToken)
	if err != nil {
		md.Delete("authorization")
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	ag, err := i.agentService.FindByToken(dsn.SecretKeyHash)
	if err != nil || ag == nil {
		md.Delete("authorization")
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	}
	if ag.Name != dsn.Name || ag.Mode != dsn.AgentMode {
		log.Errorf("failed authenticating agent (agent dsn), mismatch dsn attributes. id=%v, name=%v, mode=%v",
			ag.Id, dsn.Name, dsn.AgentMode)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, mismatch dsn attributes")
	}

	return &apitypes.Agent{ID: ag.Id, OrgID: ag.OrgId, Name: ag.Name, Mode: ag.Mode,
		Metadata: apitypes.AgentAuthMetadata{
			Hostname:      commongrpc.MetaGet(md, "hostname"),
			Platform:      commongrpc.MetaGet(md, "platform"),
			MachineID:     commongrpc.MetaGet(md, "machine_id"),
			KernelVersion: commongrpc.MetaGet(md, "kernel_version"),
			Version:       commongrpc.MetaGet(md, "version"),
			GoVersion:     commongrpc.MetaGet(md, "go-version"),
			Compiler:      commongrpc.MetaGet(md, "compiler")}}, nil
}

func parseBearerToken(md metadata.MD) (string, error) {
	t := md.Get("authorization")
	if len(t) == 0 {
		log.Debugf("missing authorization header, client-metadata=%v", md)
		return "", status.Error(codes.Unauthenticated, "invalid authentication")
	}

	tokenValue := t[0]
	tokenParts := strings.Split(tokenValue, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		log.Debugf("authorization header in wrong format, client-metadata=%v", md)
		return "", status.Error(codes.Unauthenticated, "invalid authentication")
	}

	return tokenParts[1], nil
}
