package authinterceptor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/apiclient"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/transport/adminapi"
	"github.com/runopsio/hoop/gateway/user"
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
	idp               *idp.Provider
	userService       *user.Service
	agentService      *agent.Service
	connectionService *connection.Service
}

func New(
	idpProvider *idp.Provider,
	usrsvc *user.Service,
	agentsvc *agent.Service,
	connSvc *connection.Service) grpc.StreamServerInterceptor {
	return (&interceptor{
		idp:               idpProvider,
		userService:       usrsvc,
		agentService:      agentsvc,
		connectionService: connSvc,
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
		case *types.ClientKey:
			mdCopy.Set("agent-name", fmt.Sprintf("clientkey:%s", v.Name))
			mdCopy.Set("agent-mode", pb.AgentModeEmbeddedType)
			mdCopy.Set("org-id", v.OrgID)
		}
		s.newCtx = metadata.NewIncomingContext(
			context.WithValue(ctx, GatewayContextKey{}, s.newCtxVal), mdCopy)
		return s.newCtx
	}
	return ctx
}

func (i *interceptor) StreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	log.Debugf("auth grpc interceptor, method=%v", info.FullMethod)
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

	var clientApiV2 bool
	if v := md.Get("apiv2"); len(v) > 0 {
		clientApiV2 = v[0] == "true"
	}

	var ctxVal any
	switch {
	// administrative api authentication
	case strings.HasPrefix(bearerToken, adminapi.PrefixAuthKey):
		if !adminapi.Authenticate(bearerToken) {
			log.Errorf("invalid admin api authentication, tokenlen=%v", len(bearerToken))
			return status.Errorf(codes.Unauthenticated, "failed to authenticate internal request")
		}
		// decode the user information from the header
		uctx, err := getUserInfo(md)
		if err != nil {
			return err
		}
		log.With(
			"org", uctx.OrgID, "orgname", uctx.OrgName,
			"userid", uctx.UserID, "email", uctx.UserEmail,
			"usergrps", uctx.UserGroups, "name", uctx.UserName,
			"slackid", uctx.SlackID, "status", uctx.UserStatus,
		).Infof("admin api - decoded userinfo")

		gwctx := &GatewayContext{UserContext: *uctx}
		conn, err := parseConnectionInfoFromHeader(md)
		if err != nil {
			return err
		}
		if conn != nil {
			gwctx.Connection = *conn
			log.With(
				"name", conn.Name, "type", conn.Type, "cmd", conn.CmdEntrypoint,
				"secrets", len(conn.Secrets), "agent", conn.AgentID,
			).Infof("admin api - decoded connection info")
		}
		ctxVal = gwctx
	// DEPRECATED in flavor of dsn agent keys
	// shared agent key authentication
	case strings.HasPrefix(bearerToken, "x-agt-"):
		ag, err := authenticateAgent(i.idp.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}
		ctxVal = &GatewayContext{Agent: *ag, BearerToken: bearerToken}
	// agent client keys (dsn) authentication
	// keep compatibility with old clients (hoopagent/<version>, hoopagent/sdk or hoopagent/sidecar)
	case strings.HasPrefix(commongrpc.MetaGet(md, "user-agent"), "hoopagent"):
		// TODO: deprecated in flavor of agent keys dsn
		clientKey, err := authenticateClientKeyAgent(i.idp.ApiURL, bearerToken)
		if err != nil {
			log.Error("failed validating dsn authentication, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "failed validating dsn")
		}
		if clientKey != nil {
			ctxVal = clientKey
			break
		}
		// fallback to dsn agent authentication
		ag, err := authenticateAgent(i.idp.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}

		ctxVal = &GatewayContext{Agent: *ag, BearerToken: bearerToken}
	// client proxy manager authentication (access token)
	case clientOrigin[0] == pb.ConnectionOriginClientProxyManager:
		sub, err := i.idp.VerifyAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := i.userService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		ctxVal = &GatewayContext{UserContext: *userCtx.ToAPIContext(), BearerToken: bearerToken}
	// client proxy authentication (apiv2)
	case clientApiV2:
		sub, err := i.idp.VerifyAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := i.userService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		// gwctx := &gatewayContext{UserContext: *userCtx.ToAPIContext()}
		connectionName := commongrpc.MetaGet(md, "connection-name")
		conn, err := i.getConnectionV2(bearerToken, connectionName, userCtx)
		if err != nil {
			if err == apiclient.ErrNotFound {
				return status.Errorf(codes.NotFound, "connection not found")
			}
			log.Errorf("failed obtaining connection %v, err=%v", connectionName, err)
			sentry.CaptureException(err)
			return status.Error(codes.Internal, "internal error, failed obtaining connection")
		}
		ctxVal = &GatewayContext{
			UserContext: *userCtx.ToAPIContext(),
			Connection:  *conn,
			BearerToken: bearerToken,
		}
	// client proxy authentication (access token)
	default:
		sub, err := i.idp.VerifyAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := i.userService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		gwctx := &GatewayContext{UserContext: *userCtx.ToAPIContext()}
		connectionName := commongrpc.MetaGet(md, "connection-name")
		conn, err := i.getConnection(connectionName, userCtx)
		if err != nil {
			return err
		}
		if conn == nil {
			return status.Errorf(codes.NotFound, "connection not found")
		}
		md.Set("subject", sub)
		gwctx.Connection = *conn
		ctxVal = gwctx
	}

	return handler(srv, &serverStreamWrapper{ss, nil, ctxVal})
}

// getConnectionV2 obtains connection & agent information from the node api v2
func (i *interceptor) getConnectionV2(bearerToken, name string, userCtx *user.Context) (*types.ConnectionInfo, error) {
	client := apiclient.New(i.idp.ApiURL, bearerToken)
	conn, err := client.GetConnection(name)
	if err != nil {
		return nil, err
	}
	var policies []types.PolicyInfo
	for _, p := range conn.Policies {
		policies = append(policies, types.PolicyInfo{
			ID:     p.ID,
			Name:   p.Name,
			Type:   p.Type,
			Config: p.Config,
		})
	}
	return &types.ConnectionInfo{
		ID:            conn.ID,
		Name:          conn.Name,
		Type:          conn.Type,
		CmdEntrypoint: conn.Command,
		Secrets:       conn.Secrets,
		AgentID:       conn.AgentId,
		AgentName:     conn.Agent.Name,
		AgentMode:     conn.Agent.Mode,
		Policies:      policies,
	}, nil

}

func (i *interceptor) getConnection(name string, userCtx *user.Context) (*types.ConnectionInfo, error) {
	conn, err := i.connectionService.FindOne(userCtx, name)
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
		// the agent id is not a uuid when the connection
		// is published (connectionapps) via embedded mode
		if _, err := uuid.Parse(conn.AgentId); err == nil {
			return nil, status.Errorf(codes.NotFound, "agent not found")
		}
		// keep compatibility with published agents
		ag = &agent.Agent{
			Name: fmt.Sprintf("[clientkey=%v]", strings.Split(conn.AgentId, ":")[0]), // <clientkey-name>:<connection>
			Mode: pb.AgentModeEmbeddedType,
		}
	}
	return &types.ConnectionInfo{
		ID:            conn.Id,
		Name:          conn.Name,
		Type:          string(conn.Type),
		CmdEntrypoint: conn.Command,
		Secrets:       conn.Secret,
		AgentID:       conn.AgentId,
		AgentMode:     ag.Mode,
		AgentName:     ag.Name,
	}, nil
}

func authenticateClientKeyAgent(apiURL, dsnToken string) (*types.ClientKey, error) {
	// it is an old dsn, maintain compatibility
	// <scheme>://<host>:<port>/<secretkey-hash>
	if u, _ := url.Parse(dsnToken); u != nil && len(u.Path) == 65 {
		ag, err := apiclient.New(apiURL, dsnToken).AuthClientKeys()
		if err != nil {
			return nil, err
		}
		return &types.ClientKey{
			ID:        ag.ID,
			OrgID:     ag.OrgID,
			Name:      ag.Name,
			AgentMode: ag.Mode,
			Active:    true,
		}, nil
	}
	return nil, nil
}

func authenticateAgent(apiURL, bearerToken string, md metadata.MD) (*apitypes.Agent, error) {
	reqBody := apitypes.AgentAuthRequest{
		Status: "CONNECTED",
		Metadata: &apitypes.AgentAuthMetadata{
			Hostname:      commongrpc.MetaGet(md, "hostname"),
			MachineID:     commongrpc.MetaGet(md, "machine_id"),
			KernelVersion: commongrpc.MetaGet(md, "kernel_version"),
			Version:       commongrpc.MetaGet(md, "version"),
			GoVersion:     commongrpc.MetaGet(md, "go-version"),
			Compiler:      commongrpc.MetaGet(md, "compiler"),
			Platform:      commongrpc.MetaGet(md, "platform"),
		},
	}
	ag, err := apiclient.New(apiURL, bearerToken).AuthAgent(reqBody)
	switch err {
	case apiclient.ErrUnauthorized:
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	case nil: // noop
	default:
		log.Errorf("failed validating agent authentication, reason=%v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	}
	ag.Metadata = *reqBody.Metadata
	return ag, nil
}

func getUserInfo(md metadata.MD) (*types.APIContext, error) {
	encUserInfo := md.Get(string(commongrpc.OptionUserInfo))
	if len(encUserInfo) == 0 {
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, missing system attributes")
	}
	userInfoJson, err := base64.StdEncoding.DecodeString(encUserInfo[0])
	if err != nil {
		log.Errorf("failed decoding (base64) user info: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, failed decoding (base64) user info")
	}
	var usrctx types.APIContext
	if err := json.Unmarshal(userInfoJson, &usrctx); err != nil {
		log.Errorf("failed decoding (json) user info: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, failed decoding (json) user info")
	}
	return &usrctx, nil
}

func parseConnectionInfoFromHeader(md metadata.MD) (*types.ConnectionInfo, error) {
	encConnInfo := md.Get(string(commongrpc.OptionConnectionInfo))
	if len(encConnInfo) == 0 {
		return nil, nil
	}
	connInfoJSON, err := base64.StdEncoding.DecodeString(encConnInfo[0])
	if err != nil {
		log.Errorf("failed decoding (base64) connection info: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, failed decoding (base64) connection info")
	}
	var connInfo types.ConnectionInfo
	if err := json.Unmarshal(connInfoJSON, &connInfo); err != nil {
		log.Errorf("failed decoding (json) connection info: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, failed decoding (json) connection info")
	}
	return &connInfo, nil
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
