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
	"github.com/runopsio/hoop/common/dsnkeys"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/apiclient"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	clientkeysstorage "github.com/runopsio/hoop/gateway/storagev2/clientkeys"
	"github.com/runopsio/hoop/gateway/storagev2/types"
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

	var ctxVal any
	switch {
	// administrative api authentication
	case strings.HasPrefix(bearerToken, adminApiPrefixAuthKey):
		if !authenticateAdminApi(bearerToken) {
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

		gwctx := &GatewayContext{UserContext: *uctx, IsAdminExec: true}
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
		ag, err := i.authenticateAgent(i.idp.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}
		ctxVal = &GatewayContext{Agent: *ag, BearerToken: bearerToken}
	// agent client keys (dsn) authentication
	// keep compatibility with old clients (hoopagent/<version>, hoopagent/sdk or hoopagent/sidecar)
	case strings.HasPrefix(commongrpc.MetaGet(md, "user-agent"), "hoopagent"):
		// TODO: deprecated in flavor of agent keys dsn
		clientKey, err := clientkeysstorage.ValidateDSN(storagev2.NewStorage(nil), bearerToken)
		if err != nil {
			log.Error("failed validating dsn authentication (clientkeys), err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "failed validating dsn")
		}
		if clientKey != nil {
			ctxVal = clientKey
			break
		}
		// fallback to dsn agent authentication
		ag, err := i.authenticateAgent(i.idp.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}
		org, _ := i.userService.GetOrgNameByID(ag.OrgID)
		if org == nil {
			return status.Errorf(codes.Internal, "failed obtaining organization context")
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
		userCtx, err := user.GetUserContext(i.userService, sub)
		if userCtx.User == nil {
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
		userCtx, err := user.GetUserContext(i.userService, sub)
		if userCtx.User == nil {
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
		// if gwctx.IsApiV2 {
		// 	conn, err := i.getConnectionV2(bearerToken, connectionName, userCtx)
		// 	if err != nil {
		// 		if err == apiclient.ErrNotFound {
		// 			return status.Errorf(codes.NotFound, "connection not found")
		// 		}
		// 		log.Errorf("failed obtaining connection %v, err=%v", connectionName, err)
		// 		sentry.CaptureException(err)
		// 		return status.Error(codes.Internal, "internal error, failed obtaining connection")
		// 	}
		// 	gwctx.Connection = *conn
		// 	ctxVal = gwctx
		// 	break
		// }
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

// getConnectionV2 obtains connection & agent information from the node api v2
// func (i *interceptor) getConnectionV2(bearerToken, name string, userCtx *user.Context) (*types.ConnectionInfo, error) {
// 	client := apiclient.New(bearerToken)
// 	conn, err := client.GetConnection(name)
// 	if err != nil {
// 		return nil, err
// 	}
// 	var policies []types.PolicyInfo
// 	for _, p := range conn.Policies {
// 		policies = append(policies, types.PolicyInfo{
// 			ID:     p.ID,
// 			Name:   p.Name,
// 			Type:   p.Type,
// 			Config: p.Config,
// 		})
// 	}
// 	return &types.ConnectionInfo{
// 		ID:            conn.ID,
// 		Name:          conn.Name,
// 		Type:          conn.Type,
// 		CmdEntrypoint: conn.Command,
// 		Secrets:       conn.Secrets,
// 		AgentID:       conn.AgentId,
// 		AgentName:     conn.Agent.Name,
// 		AgentMode:     conn.Agent.Mode,
// 		Policies:      policies,
// 	}, nil
// }

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

func (i *interceptor) authenticateAgent(apiURL, bearerToken string, md metadata.MD) (*apitypes.Agent, error) {
	if strings.HasPrefix(bearerToken, "x-agt-") {
		ag, err := i.agentService.FindByToken(bearerToken)
		if err != nil || ag == nil {
			md.Delete("authorization")
			log.Debugf("invalid agent authentication (legacy auth), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
			return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		return &apitypes.Agent{ID: ag.Id, OrgID: ag.OrgId, Name: ag.Name, Mode: ag.Mode,
			Metadata: apitypes.AgentAuthMetadata{
				Hostname:      ag.Hostname,
				Platform:      ag.Platform,
				MachineID:     ag.MachineId,
				KernelVersion: ag.KernelVersion,
				Version:       ag.Version,
				GoVersion:     ag.GoVersion,
				Compiler:      ag.Compiler}}, nil
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
	var pa pgrest.Agent
	err = pgrest.New("/agents?id=eq.%s", ag.Id).Patch(map[string]any{
		"status": "CONNECTED",
		"metadata": map[string]string{
			"hostname":       commongrpc.MetaGet(md, "hostname"),
			"machine_id":     commongrpc.MetaGet(md, "machine_id"),
			"kernel_version": commongrpc.MetaGet(md, "kernel_version"),
			"version":        commongrpc.MetaGet(md, "version"),
			"goversion":      commongrpc.MetaGet(md, "go-version"),
			"compiler":       commongrpc.MetaGet(md, "compiler"),
			"platform":       commongrpc.MetaGet(md, "platform"),
		},
	}).DecodeInto(&pa)
	if err != nil {
		log.Errorf("failed updating agent status. id=%v, name=%v, err=%v", ag.Id, dsn.Name, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, failed updating agent status")
	}
	return &apitypes.Agent{ID: pa.ID, OrgID: pa.OrgID, Name: pa.Name, Mode: pa.Mode,
		Metadata: apitypes.AgentAuthMetadata{
			Hostname:      pa.GetMeta("hostname"),
			Platform:      pa.GetMeta("platform"),
			MachineID:     pa.GetMeta("machine_id"),
			KernelVersion: pa.GetMeta("kernel_version"),
			Version:       pa.GetMeta("version"),
			GoVersion:     pa.GetMeta("goversion"),
			Compiler:      pa.GetMeta("compiler")}}, nil
}

func authenticateClientKeyAgent(apiURL, dsnToken string) (*types.ClientKey, error) {
	// it is an old dsn, maintain compatibility
	// <scheme>://<host>:<port>/<secretkey-hash>
	if u, _ := url.Parse(dsnToken); u != nil && len(u.Path) == 65 {
		ag, err := apiclient.New(dsnToken).AuthClientKeys()
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
