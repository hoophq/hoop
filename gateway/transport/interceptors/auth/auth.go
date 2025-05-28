package authinterceptor

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/dsnkeys"
	commongrpc "github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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
	idp *idp.Provider
}

func New(idpProvider *idp.Provider) grpc.StreamServerInterceptor {
	return (&interceptor{
		idp: idpProvider,
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
				mdCopy.Set("connection-subtype", v.Connection.SubType)
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
	clientOrigin, clientVerb := md.Get("origin"), md.Get("verb")
	if len(clientOrigin) == 0 {
		md.Delete("authorization")
		log.Debugf("client missing origin, client-metadata=%v", md)
		return status.Error(codes.InvalidArgument, "missing client origin")
	}

	if len(clientVerb) > 0 && clientVerb[0] == pb.ClientVerbPlainExec {
		plainExecKey := md.Get("plain-exec-key")
		if len(plainExecKey) == 0 || plainExecKey[0] != clientexec.PlainExecSecretKey {
			errMsg := "failed validating plain execution, plain-exec-key attribute is missing or does not match"
			log.Error(errMsg)
			sentry.CaptureException(errors.New(errMsg))
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
	}

	bearerToken, err := parseBearerToken(md)
	if err != nil {
		return err
	}

	var ctxVal any
	switch clientOrigin[0] {
	case pb.ConnectionOriginAgent:
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
	case pb.ConnectionOriginClientProxyManager:
		subject, err := i.validateAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := models.GetUserContext(subject)
		if err != nil {
			log.Errorf("failed fetching user context, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		if userCtx.IsEmpty() {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		if userCtx.UserStatus != string(types.UserStatusActive) {
			return status.Errorf(codes.Unauthenticated, "user is not active")
		}

		// Maintain backward compatibility with legacy user context propagation.
		// This behavior was preserved during migration from XTDB to PostgreSQL.
		// TODO: Refactor to implement proper user information propagation in a future release.
		userCtx.UserID = userCtx.UserSubject
		ctxVal = &GatewayContext{
			UserContext: *userCtx,
			BearerToken: bearerToken,
		}
	// client proxy authentication (access token)
	default:
		apiKeyEnv := os.Getenv("API_KEY")
		isOrgMultitenant := appconfig.Get().OrgMultitenant()
		// this is a not so optimal solution, but due to the overall
		// complexity of the authentication system, we decided to make this
		// simple comparison on a optimistic way and if it fails, we fallback
		// to the regular authentication flow with the IDP (see else stetament)
		if apiKeyEnv != "" && apiKeyEnv == bearerToken && !isOrgMultitenant {
			log.Debug("Authenticating with API key")
			orgID := strings.Split(bearerToken, "|")[0]
			org, err := models.GetOrganizationByNameOrID(orgID)
			if err != nil {
				return status.Errorf(codes.Unauthenticated, "invalid authentication")
			}
			deterministicUuid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(`API_KEY`))
			ctx := &models.Context{
				OrgID:          orgID,
				OrgName:        org.Name,
				OrgLicenseData: org.LicenseData,
				UserID:         deterministicUuid.String(),
				UserSubject:    "API_KEY",
				UserName:       "API_KEY",
				UserEmail:      "API_KEY",
				UserStatus:     "active",
				UserGroups:     []string{types.GroupAdmin},
			}

			gwctx := &GatewayContext{
				UserContext: *ctx,
				BearerToken: bearerToken,
			}

			connectionName := commongrpc.MetaGet(md, "connection-name")
			conn, err := i.getConnection(connectionName, ctx)
			if err != nil {
				return err
			}
			if conn == nil {
				return status.Errorf(codes.NotFound, "connection not found")
			}
			gwctx.Connection = *conn
			ctxVal = gwctx
			break
		}
		// first we check if the auth method is local, if so, we authenticate the user
		// using the local auth method, otherwise we use the i.idp.VerifyAccessToken
		subject, err := i.validateAccessToken(bearerToken)
		if err != nil {
			log.Debugf("failed verifying access token, reason=%v", err)
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		userCtx, err := models.GetUserContext(subject)
		if err != nil || userCtx.IsEmpty() {
			return status.Errorf(codes.Unauthenticated, "invalid authentication")
		}

		if userCtx.UserStatus != string(types.UserStatusActive) {
			return status.Errorf(codes.Unauthenticated, "user is not active")
		}

		// Maintain backward compatibility with legacy user context propagation.
		// This behavior was preserved during migration from XTDB to PostgreSQL.
		// TODO: Refactor to implement proper user information propagation in a future release.
		userCtx.UserID = userCtx.UserSubject
		gwctx := &GatewayContext{
			UserContext: *userCtx,
			BearerToken: bearerToken,
		}
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

func (i *interceptor) validateAccessToken(bearerToken string) (subject string, err error) {
	if i.idp.HasSecretKey() {
		return i.idp.VerifyAccessTokenHS256Alg(bearerToken)
	}
	return i.idp.VerifyAccessToken(bearerToken)
}

func (i *interceptor) getConnection(name string, userCtx *models.Context) (*types.ConnectionInfo, error) {
	conn, err := models.GetConnectionByNameOrID(userCtx, name)
	if err != nil {
		log.Errorf("failed retrieving connection %v, err=%v", name, err)
		sentry.CaptureException(err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain connection")
	}
	if conn == nil {
		return nil, nil
	}
	return &types.ConnectionInfo{
		ID:                               conn.ID,
		Name:                             conn.Name,
		Type:                             string(conn.Type),
		SubType:                          conn.SubType.String,
		Command:                          conn.Command,
		Secrets:                          conn.AsSecrets(),
		Tags:                             conn.ConnectionTags,
		AgentID:                          conn.AgentID.String,
		AgentMode:                        conn.AgentMode,
		AgentName:                        conn.AgentName,
		AccessModeRunbooks:               conn.AccessModeRunbooks,
		AccessModeExec:                   conn.AccessModeExec,
		AccessModeConnect:                conn.AccessModeConnect,
		AccessSchema:                     conn.AccessSchema,
		JiraTransitionNameOnSessionClose: conn.JiraTransitionNameOnClose.String,
	}, nil
}

func (i *interceptor) authenticateAgent(bearerToken string, md metadata.MD) (*models.Agent, error) {
	if strings.HasPrefix(bearerToken, "x-agt-") {
		ag, err := models.GetAgentByToken(bearerToken)
		if err != nil {
			md.Delete("authorization")
			log.Debugf("invalid agent authentication (legacy auth), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
			return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
		}
		return ag, nil
	}
	dsn, err := dsnkeys.Parse(bearerToken)
	if err != nil {
		md.Delete("authorization")
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	ag, err := models.GetAgentByToken(dsn.SecretKeyHash)
	if err != nil {
		md.Delete("authorization")
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, client-metadata=%v, err=%v", len(bearerToken), md, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication")
	}
	if ag.Name != dsn.Name {
		log.Errorf("failed authenticating agent (agent dsn), mismatch dsn attributes. id=%v, name=%v, mode=%v",
			ag.ID, dsn.Name, dsn.AgentMode)
		return nil, status.Errorf(codes.Unauthenticated, "invalid authentication, mismatch dsn attributes")
	}
	return ag, nil
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
