package transport

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
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/transport/adminapi"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type gatewayContext struct {
	UserContext types.APIContext
	Connection  types.ConnectionInfo
	Agent       apitypes.Agent

	bearerToken string
}

func (c *gatewayContext) ValidateConnectionAttrs() error {
	if c.Connection.Name == "" || c.Connection.AgentID == "" ||
		c.Connection.ID == "" || c.Connection.Type == "" {
		return status.Error(codes.InvalidArgument, "missing required connection attributes")
	}
	return nil
}

type wrappedStream struct {
	grpc.ServerStream

	newCtx    context.Context
	newCtxVal any
}

type gatewayContextKey struct{}

// https://github.com/grpc/grpc-go/issues/4363#issuecomment-840030503
func (w *wrappedStream) Context() context.Context {
	if w.newCtx != nil {
		return w.newCtx
	}
	ctx := w.ServerStream.Context()
	if w.newCtxVal == nil {
		return ctx
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		w.newCtx = metadata.NewIncomingContext(
			context.WithValue(ctx, gatewayContextKey{}, w.newCtxVal), md.Copy())
		return w.newCtx
	}
	return ctx
}

func getGatewayContext(ctx context.Context) (any, error) {
	if ctx == nil {
		return nil, status.Error(codes.Internal, "authentication context not found (nil)")
	}
	val := ctx.Value(gatewayContextKey{})
	if val == nil {
		return nil, status.Error(codes.Internal, "authentication context not found")
	}
	return val, nil
}

func parseGatewayContextInto(ctx context.Context, into any) error {
	val, err := getGatewayContext(ctx)
	if err != nil {
		return err
	}
	var assigned bool
	switch v := val.(type) {
	case *gatewayContext:
		if _, ok := into.(*gatewayContext); ok {
			*into.(*gatewayContext) = *v
			assigned = true
		}
	case *types.ClientKey:
		if _, ok := into.(*types.ClientKey); ok {
			*into.(*types.ClientKey) = *v
			assigned = true
		}
	case *apitypes.Agent:
		if _, ok := into.(*agent.Agent); ok {
			*into.(*apitypes.Agent) = *v
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

func (s *Server) AuthGrpcInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
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

		gwctx := &gatewayContext{UserContext: *uctx}
		conn, err := getConnectionInfo(md)
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
		ag, err := authenticateAgent(s.IDProvider.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}
		ctxVal = &gatewayContext{Agent: *ag, bearerToken: bearerToken}
	// agent client keys (dsn) authentication
	// keep compatibility with old clients (hoopagent/<version>, hoopagent/sdk or hoopagent/sidecar)
	case strings.HasPrefix(mdget(md, "user-agent"), "hoopagent"):
		// TODO: deprecated in flavor of agent keys dsn
		clientKey, err := authenticateClientKeyAgent(s.IDProvider.ApiURL, bearerToken)
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
		ag, err := authenticateAgent(s.IDProvider.ApiURL, bearerToken, md)
		if err != nil {
			return err
		}
		ctxVal = &gatewayContext{Agent: *ag, bearerToken: bearerToken}
	// client proxy manager authentication (access token)
	case clientOrigin[0] == pb.ConnectionOriginClientProxyManager:
		sub, err := s.exchangeUserToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := s.UserService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		ctxVal = &gatewayContext{UserContext: *userCtx.ToAPIContext(), bearerToken: bearerToken}
	// client proxy authentication (apiv2)
	case clientApiV2:
		sub, err := s.exchangeUserToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := s.UserService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		// gwctx := &gatewayContext{UserContext: *userCtx.ToAPIContext()}
		connectionName := mdget(md, "connection-name")
		conn, err := s.getConnectionV2(bearerToken, connectionName, userCtx)
		if err != nil {
			if err == apiclient.ErrNotFound {
				return status.Errorf(codes.NotFound, "connection not found")
			}
			sentry.CaptureException(err)
			log.Errorf("failed obtaining connection %v, err=%v", connectionName, err)
			return status.Error(codes.Internal, "internal error, failed obtaining connection")
		}
		ctxVal = &gatewayContext{
			UserContext: *userCtx.ToAPIContext(),
			Connection:  *conn,
			bearerToken: bearerToken,
		}
	// client proxy authentication (access token)
	default:
		sub, err := s.exchangeUserToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := s.UserService.FindBySub(sub)
		if err != nil || userCtx.User == nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		gwctx := &gatewayContext{UserContext: *userCtx.ToAPIContext()}
		connectionName := mdget(md, "connection-name")
		conn, err := s.getConnection(bearerToken, connectionName, userCtx)
		if err != nil {
			return err
		}
		if conn == nil {
			return status.Errorf(codes.NotFound, "connection not found")
		}
		gwctx.Connection = *conn
		ctxVal = gwctx
	}

	return handler(srv, &wrappedStream{ss, nil, ctxVal})
}

// getConnectionV2 obtains connection & agent information from the node api v2
func (s *Server) getConnectionV2(bearerToken, name string, userCtx *user.Context) (*types.ConnectionInfo, error) {
	client := apiclient.New(s.IDProvider.ApiURL, bearerToken)
	conn, err := client.GetConnection(name)
	if err != nil {
		if err == apiclient.ErrNotFound {
			return nil, status.Errorf(codes.NotFound, "connection not found")
		}
		return nil, status.Error(codes.Internal, "internal error, failed obtaining connection")
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
	}, nil

}

func (s *Server) getConnection(bearerToken, name string, userCtx *user.Context) (*types.ConnectionInfo, error) {
	if conn, _ := apiclient.New(s.IDProvider.ApiURL, bearerToken).
		GetConnection(name); conn != nil {
		log.Infof("obtained connection %v from apiv2", name)
		return &types.ConnectionInfo{
			ID:            conn.ID,
			Name:          conn.Name,
			Type:          conn.Type,
			CmdEntrypoint: conn.Command,
			Secrets:       conn.Secrets,
			AgentID:       conn.AgentId,
			// TODO: add additional agent attributes
		}, nil
	}
	conn, err := s.ConnectionService.FindOne(userCtx, name)
	if err != nil {
		log.Errorf("failed retrieving connection %v, err=%v", name, err)
		sentry.CaptureException(err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain connection")
	}
	if conn == nil {
		return nil, nil
	}
	ag, err := s.AgentService.FindByNameOrID(userCtx, conn.AgentId)
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
			Hostname:      mdget(md, "hostname"),
			MachineID:     mdget(md, "machine_id"),
			KernelVersion: mdget(md, "kernel_version"),
			Version:       mdget(md, "version"),
			GoVersion:     mdget(md, "go-version"),
			Compiler:      mdget(md, "compiler"),
			Platform:      mdget(md, "platform"),
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
	return ag, nil
}

func publishAgentDisconnect(apiURL, bearerToken string) error {
	reqBody := apitypes.AgentAuthRequest{Status: "DISCONNECTED"}
	_, err := apiclient.New(apiURL, bearerToken).AuthAgent(reqBody)
	return err
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

func getConnectionInfo(md metadata.MD) (*types.ConnectionInfo, error) {
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

func parseToLegacyUserContext(apictx *types.APIContext) *user.Context {
	return &user.Context{
		Org: &user.Org{
			Id:   apictx.OrgID,
			Name: apictx.OrgName,
		},
		User: &user.User{
			Id:      apictx.UserID,
			Org:     apictx.OrgID,
			Name:    apictx.UserName,
			Email:   apictx.UserEmail,
			Status:  user.StatusType(apictx.UserStatus),
			SlackID: apictx.SlackID, // TODO: check this
			Groups:  apictx.UserGroups,
		},
	}
}
