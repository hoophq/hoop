package apiconnections

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/httpproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/postgresproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/ssmproxy"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

var validConnectionTypes = []string{"postgres", "ssh", "rdp", "aws-ssm", "httpproxy", "kubernetes", "claude-code"}

// CreateConnectionCredentials
//
//	@Summary		Create Connection Credentials
//	@Description	Create Connection Credentials
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string									true	"Name or UUID of the connection"
//	@Param			request		body		openapi.ConnectionCredentialsRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.ConnectionCredentialsResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/credentials [post]
func CreateConnectionCredentials(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// Lazy cleanup of expired credential sessions
	err := models.CloseExpiredCredentialSessions()
	if err != nil {
		log.Errorf("failed to close expired credential sessions, err=%v", err)
	}

	var req openapi.ConnectionCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}

	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		c.AbortWithStatusJSON(500, gin.H{"message": fmt.Sprintf("failed to retrieve server config, err=%v", err)})
		return
	}

	connNameOrID := c.Param("nameOrID")
	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.AbortWithStatusJSON(404, gin.H{"message": fmt.Sprintf("connection %s not found", connNameOrID)})
		return
	}

	// this is for map the (grafana, kibana and kubernetes-token) subtype to http-proxy
	subtype := mapValidSubtypeToHttpProxy(conn)
	conn.SubType = sql.NullString{String: subtype.String(), Valid: true}

	if !slices.Contains(validConnectionTypes, conn.SubType.String) {
		c.AbortWithStatusJSON(400, gin.H{"message": "connection subtype is not supported for this connection"})
		return
	}

	if !isConnectionTypeConfigured(proto.ConnectionType(conn.SubType.String)) {
		c.AbortWithStatusJSON(400, gin.H{"message": "Listening address is not configured for this connection type"})
		return
	}

	if conn.AccessModeConnect != "enabled" {
		c.AbortWithStatusJSON(400, gin.H{"message": "access mode connect is not enabled for this connection"})
		return
	}

	// Create session for audit trail
	sid := uuid.NewString()
	newSession := models.Session{
		ID:                sid,
		OrgID:             ctx.OrgID,
		UserEmail:         ctx.UserEmail,
		UserID:            ctx.UserID,
		UserName:          ctx.UserName,
		Connection:        conn.Name,
		ConnectionType:    conn.Type,
		ConnectionSubtype: conn.SubType.String,
		ConnectionTags:    conn.ConnectionTags,
		Verb:              proto.ClientVerbConnect,
		Status:            string(openapi.SessionStatusOpen),
		CreatedAt:         time.Now().UTC(),
	}

	// Check if connection requires review/JIT approval
	requiresReview, accessRule := checkConnectionRequiresReview(ctx, conn)

	// Persist session
	if err := models.UpsertSession(newSession); err != nil {
		log.Errorf("failed creating session, err=%v", err)

		c.AbortWithStatusJSON(500, gin.H{"message": "failed creating session"})
		return
	}

	// If review/JIT is required, create review record and return 202
	if requiresReview {
		reviewID, err := createConnectionCredentialsReview(ctx, conn, accessRule, sid, req.AccessDurationSec)
		if err != nil {
			log.Errorf("failed creating review, err=%v", err)
			c.AbortWithStatusJSON(500, gin.H{"message": "failed creating review"})
			return
		}

		// Return 202 with session_id and review_id, NO credentials
		c.JSON(202, &openapi.ConnectionCredentialsResponse{
			ConnectionName:    conn.Name,
			ConnectionType:    conn.Type,
			ConnectionSubType: conn.SubType.String,
			SessionID:         sid,
			HasReview:         true,
			ReviewID:          reviewID,
			CreatedAt:         time.Now().UTC(),
			ExpireAt:          time.Now().UTC().Add(time.Duration(req.AccessDurationSec) * time.Second),
		})
		return
	}

	connType := proto.ConnectionType(conn.SubType.String)
	secretKey, secretKeyHash, err := generateSecretKey(connType)
	if err != nil {
		log.Warnf("failed to create access credentials, err=%v", err)
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}

	expireAt := time.Now().UTC().Add(time.Duration(req.AccessDurationSec) * time.Second)
	if expireAt.After(time.Now().UTC().Add(48 * time.Hour)) {
		c.AbortWithStatusJSON(400, gin.H{"message": "access duration cannot exceed 48 hours"})
		return
	}

	db, err := models.CreateConnectionCredentials(&models.ConnectionCredentials{
		ID:             uuid.NewString(),
		OrgID:          ctx.OrgID,
		UserSubject:    ctx.UserID,
		ConnectionName: conn.Name,
		ConnectionType: proto.ToConnectionType(conn.Type, conn.SubType.String).String(),
		SecretKeyHash:  secretKeyHash,
		SessionID:      sid,
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       expireAt,
	})
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	// Store credential expiry in session metadata so the frontend can display it
	if err := models.SetSessionCredentialsExpireAt(ctx.OrgID, sid, db.ExpireAt); err != nil {
		log.Warnf("failed setting session credentials expire_at metadata, err=%v", err)
	}

	c.JSON(201, buildConnectionCredentialsResponse(db, conn, serverConf, secretKey, false, ""))
}

// ResumeConnectionCredentials
//
//	@Summary		Resume Connection Credentials Request
//	@Description	Resume a connection credentials request after review approval
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string									true	"Name or UUID of the connection"
//	@Param			sessionID	path		string									true	"Session ID from the initial request"
//	@Param			request		body		openapi.ConnectionCredentialsRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.ConnectionCredentialsResponse
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/credentials/{sessionID} [post]
func ResumeConnectionCredentials(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// Lazy cleanup of expired credential sessions
	err := models.CloseExpiredCredentialSessions()
	if err != nil {
		log.Errorf("failed to close expired credential sessions, err=%v", err)
	}

	var req openapi.ConnectionCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}

	connNameOrID := c.Param("nameOrID")
	sessionID := c.Param("ID")

	// Look up the session
	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	switch err {
	case models.ErrNotFound:
		c.AbortWithStatusJSON(404, gin.H{"message": "session not found"})
		return
	case nil:
		// Verify session belongs to user
		if session.UserID != ctx.UserID {
			c.AbortWithStatusJSON(403, gin.H{"message": "session does not belong to user"})
			return
		}
	default:
		log.Errorf("failed fetching session, err=%v", err)
		c.AbortWithStatusJSON(500, gin.H{"message": "failed fetching session"})
		return
	}

	// Look up the review
	review, err := models.GetReviewByIdOrSid(ctx.OrgID, sessionID)
	if err != nil && err != models.ErrNotFound {
		log.Errorf("failed fetching review, err=%v", err)
		c.AbortWithStatusJSON(500, gin.H{"message": "failed fetching review"})
		return
	}
	if review == nil {
		c.AbortWithStatusJSON(404, gin.H{"message": "review not found for this session"})
		return
	}

	// Gate on review status: only APPROVED reviews may proceed
	switch review.Status {
	case models.ReviewStatusPending:
		c.JSON(202, gin.H{
			"message":    "review is still pending approval",
			"session_id": sessionID,
			"review_id":  review.ID,
			"status":     review.Status,
		})
		return
	case models.ReviewStatusRejected:
		c.AbortWithStatusJSON(403, gin.H{"message": "review was rejected"})
		return
	case models.ReviewStatusApproved:
		// continue — use session status as the source of truth below
	default:
		c.AbortWithStatusJSON(400, gin.H{"message": fmt.Sprintf("invalid review status: %s", review.Status)})
		return
	}

	// Use session status as the source of truth for credential state:
	//   ready → review approved, credentials not yet issued (first call after approval)
	//   open  → credentials already issued and active (rotate key on repeated calls)
	//   done  → session closed (credentials expired or review rejected/revoked)
	if session.Status == string(openapi.SessionStatusDone) {
		c.AbortWithStatusJSON(410, gin.H{"message": "credentials have expired"})
		return
	}

	// Check if credentials already exist for this session to preserve the original ExpireAt
	existingCred, err := models.GetConnectionCredentialsBySessionID(ctx.OrgID, sessionID)
	if err != nil && err != models.ErrNotFound {
		log.Errorf("failed checking existing credentials, err=%v", err)
		c.AbortWithStatusJSON(500, gin.H{"message": "failed checking existing credentials"})
		return
	}

	// Determine ExpireAt:
	//   first call (session=ready, existingCred=nil) → calculate from review duration
	//   repeated call (session=open, existingCred!=nil) → preserve original timer
	var expireAt time.Time
	var createdAt time.Time
	if existingCred != nil {
		if existingCred.ExpireAt.Before(time.Now().UTC()) {
			c.AbortWithStatusJSON(410, gin.H{"message": "credentials have expired"})
			return
		}
		expireAt = existingCred.ExpireAt
		createdAt = existingCred.CreatedAt
		log.With("session_id", sessionID).Infof("reusing existing credential expiration: %v", expireAt.Format(time.RFC3339))
	} else {
		expireAt = time.Now().UTC().Add(time.Duration(review.AccessDurationSec) * time.Second)
		createdAt = time.Now().UTC()
	}

	// Get connection
	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.AbortWithStatusJSON(404, gin.H{"message": fmt.Sprintf("connection %s not found", connNameOrID)})
		return
	}

	// Verify connection matches the review
	if conn.Name != review.ConnectionName {
		c.AbortWithStatusJSON(400, gin.H{"message": "connection name does not match review"})
		return
	}

	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		c.AbortWithStatusJSON(500, gin.H{"message": fmt.Sprintf("failed to retrieve server config, err=%v", err)})
		return
	}

	// Map subtype
	subtype := mapValidSubtypeToHttpProxy(conn)
	conn.SubType = sql.NullString{String: subtype.String(), Valid: true}

	if !slices.Contains(validConnectionTypes, conn.SubType.String) {
		c.AbortWithStatusJSON(400, gin.H{"message": "connection subtype is not supported for this connection"})
		return
	}

	if !isConnectionTypeConfigured(proto.ConnectionType(conn.SubType.String)) {
		c.AbortWithStatusJSON(400, gin.H{"message": "Listening address is not configured for this connection type"})
		return
	}

	// Generate credentials
	connType := proto.ConnectionType(conn.SubType.String)
	secretKey, secretKeyHash, err := generateSecretKey(connType)
	if err != nil {
		log.Warnf("failed to create access credentials, err=%v", err)
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}

	// If credentials already exist for this session, update the secret key (preserves expiration)
	// Otherwise create a new record
	var db *models.ConnectionCredentials
	if existingCred != nil {
		if err := models.UpdateConnectionCredentialsSecretKey(existingCred.ID, secretKeyHash); err != nil {
			c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
			return
		}
		existingCred.SecretKeyHash = secretKeyHash
		db = existingCred
	} else {
		db, err = models.CreateConnectionCredentials(&models.ConnectionCredentials{
			ID:             uuid.NewString(),
			OrgID:          ctx.OrgID,
			UserSubject:    ctx.UserID,
			ConnectionName: conn.Name,
			ConnectionType: proto.ToConnectionType(conn.Type, conn.SubType.String).String(),
			SecretKeyHash:  secretKeyHash,
			SessionID:      sessionID,
			CreatedAt:      createdAt,
			ExpireAt:       expireAt,
		})
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
			return
		}
	}

	// Transition session to "open" the first time credentials are issued (session was "ready")
	if session.Status == string(openapi.SessionStatusReady) {
		if err := models.UpdateSessionStatus(ctx.OrgID, sessionID, string(openapi.SessionStatusOpen)); err != nil {
			log.Warnf("failed updating session status to open, err=%v", err)
		}
	}

	// Store credential expiry in session metadata so the frontend can display it
	if err := models.SetSessionCredentialsExpireAt(ctx.OrgID, sessionID, db.ExpireAt); err != nil {
		log.Warnf("failed setting session credentials expire_at metadata, err=%v", err)
	}

	c.JSON(201, buildConnectionCredentialsResponse(db, conn, serverConf, secretKey, false, ""))
}

// RevokeConnectionCredentials
//
//	@Summary		Revoke Connection Credentials
//	@Description	Revokes a connection credential, invalidating it and disconnecting any active sessions
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID		path		string	true	"Name or UUID of the connection"
//	@Param			credentialID	path		string	true	"UUID of the credential to revoke"
//	@Success		204			"No content"
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/credentials/{credentialID}/revoke [post]
func RevokeConnectionCredentials(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	credentialID := c.Param("ID")

	if credentialID == "" {
		c.AbortWithStatusJSON(400, gin.H{"message": "credential ID is required"})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.AbortWithStatusJSON(404, gin.H{"message": fmt.Sprintf("connection %s not found", connNameOrID)})
		return
	}

	cred, err := models.GetConnectionCredentialsByID(ctx.OrgID, credentialID)
	if err != nil {
		if err == models.ErrNotFound {
			c.AbortWithStatusJSON(404, gin.H{"message": fmt.Sprintf("credential %s not found", credentialID)})
			return
		}
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	if cred.ConnectionName != conn.Name {
		c.AbortWithStatusJSON(404, gin.H{"message": fmt.Sprintf("credential %s does not belong to connection %s", credentialID, connNameOrID)})
		return
	}

	// Only the credential owner or org admin can revoke
	if cred.UserSubject != ctx.UserID && !ctx.IsAdmin() {
		c.AbortWithStatusJSON(403, gin.H{"message": "only the credential owner or admin can revoke"})
		return
	}

	if err := models.RevokeConnectionCredentials(ctx.OrgID, credentialID); err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": fmt.Sprintf("failed to revoke credential: %v", err)})
		return
	}

	if cred.SessionID != "" {
		if err := models.SetSessionCredentialsRevokedAt(ctx.OrgID, cred.SessionID, time.Now().UTC()); err != nil {
			log.Warnf("failed setting session credentials revoked_at metadata, err=%v", err)
		}
	}

	// Cancel active sessions in each proxy
	connType := proto.ConnectionType(cred.ConnectionType)
	switch connType {
	case proto.ConnectionTypePostgres:
		postgresproxy.GetServerInstance().RevokeByCredentialID(credentialID)
	case proto.ConnectionTypeSSH:
		sshproxy.GetServerInstance().RevokeByCredentialID(credentialID)
	case proto.ConnectionTypeRDP:
		broker.RevokeByCredentialID(credentialID)
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes, proto.ConnectionTypeClaudeCode, proto.ConnectionTypeCommandLine:
		httpproxy.GetServerInstance().RevokeBySecretKeyHash(cred.SecretKeyHash)
	case proto.ConnectionTypeSSM:
		// SSM has no persistent session store; DB invalidation blocks new connections
	}

	c.Status(204)
}

func mapValidSubtypeToHttpProxy(conn *models.Connection) proto.ConnectionType {
	switch conn.SubType.String {
	case "grafana", "kibana", "kubernetes-token":
		return proto.ConnectionTypeHttpProxy
	case "git", "github":
		return proto.ConnectionTypeSSH
	case "kubernetes", "kubernetes-eks":
		return proto.ConnectionTypeKubernetes
	default:
		return proto.ConnectionType(conn.SubType.String)
	}
}

// toConnectionType maps the connection type and subtype to the appropriate proto.ConnectionType
// This is because we have some connection types that are represented as subtypes in the database.
// The decap uses the subtype to determine the actual connection type.
// for keep the code consistent with other places, we keep this mapping logic here.
// but basically some stuff happen in the frontend base on the (connectionType, subtype) pair, but for
// the backend we just need the final connection type.
func toConnectionType(connectionType, subtype string) proto.ConnectionType {
	switch connectionType {
	case "command-line":
		switch subtype {
		case "kubernetes", "kubernetes-eks":
			return proto.ConnectionType(proto.ConnectionTypeKubernetes)
		case "kubernetes-token", "httpproxy":
			return proto.ConnectionType(proto.ConnectionTypeHttpProxy)
		}
	}

	return proto.ConnectionType(connectionType)
}

func buildConnectionCredentialsResponse(
	cred *models.ConnectionCredentials,
	conn *models.Connection,
	serverConf *models.ServerMiscConfig,
	secretKey string,
	hasReview bool,
	reviewID string) *openapi.ConnectionCredentialsResponse {
	const dummyString = "hoop"

	base := openapi.ConnectionCredentialsResponse{
		ID:                cred.ID,
		ConnectionType:    conn.Type,
		ConnectionName:    cred.ConnectionName,
		ConnectionSubType: conn.SubType.String,
		SessionID:         cred.SessionID,
		HasReview:         hasReview,
		ReviewID:          reviewID,
		CreatedAt:         cred.CreatedAt,
		ExpireAt:          cred.ExpireAt,
	}

	connectionType := toConnectionType(cred.ConnectionType, conn.SubType.String)
	serverHost, serverPort := getServerHostAndPort(serverConf, connectionType)

	switch connectionType {
	case proto.ConnectionTypePostgres:
		var databaseName string
		defaultDBEnc := conn.Envs["envvar:DB"]
		if defaultDBEnc != "" {
			defaultDBBytes, _ := base64.StdEncoding.DecodeString(defaultDBEnc)
			databaseName = string(defaultDBBytes)
		}
		if databaseName == "" {
			databaseName = "postgres"
		}
		sslMode := "disable"
		if appconfig.Get().GatewayTLSKey() != "" {
			sslMode = "require"
		}
		base.ConnectionCredentials = &openapi.PostgresConnectionInfo{
			Hostname:     serverHost,
			Port:         serverPort,
			Username:     secretKey,
			Password:     dummyString,
			DatabaseName: databaseName,
			ConnectionString: fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
				secretKey, dummyString, serverHost, serverPort, databaseName, sslMode),
		}
	case proto.ConnectionTypeSSH:
		base.ConnectionCredentials = &openapi.SSHConnectionInfo{
			Hostname: serverHost,
			Port:     serverPort,
			Username: dummyString,
			Password: secretKey,
			Command:  fmt.Sprintf("sshpass -p '%s' ssh %s@%s -p %s", dummyString, secretKey, serverHost, serverPort),
		}
	case proto.ConnectionTypeRDP:
		base.ConnectionCredentials = &openapi.RDPConnectionInfo{
			Hostname: serverHost,
			Port:     serverPort,
			Username: secretKey,
			Password: secretKey,
			Command:  fmt.Sprintf("xfreerdp /v:%s:%s /u:%s /p:%s", serverHost, serverPort, secretKey, secretKey),
		}
	case proto.ConnectionTypeSSM:
		accessKeyId, err := ssmproxy.UUIDToAccessKey(cred.ID)
		if err != nil {
			log.Errorf("failed to convert connection id to access key, err=%v", err) // Should NOT happen
			return nil
		}

		if len(cred.SecretKeyHash) < 40 {
			// Realistically, this should never happen
			log.Errorf("invalid secret key hash, reason=%v", err)
			return nil
		}

		endpoint := fmt.Sprintf("%s/ssm/", appconfig.Get().ApiURL())
		// We pass hash here, since it's used for signing
		// Trimmed secret key since AWS only handles 40 characters
		accessSecret := cred.SecretKeyHash[:40]
		base.ConnectionCredentials = &openapi.SSMConnectionInfo{
			EndpointURL:        endpoint,
			AwsAccessKeyId:     accessKeyId,
			AwsSecretAccessKey: accessSecret,
			ConnectionString: fmt.Sprintf(
				"AWS_ACCESS_KEY_ID=%q AWS_SECRET_ACCESS_KEY=%q aws ssm start-session --target {TARGET_INSTANCE} --endpoint-url %q",
				accessKeyId, accessSecret, endpoint),
		}
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes:
		scheme := "http"
		host := serverHost
		if appconfig.Get().GatewayTLSKey() != "" {
			scheme = "https"
			// When TLS is enabled, use the API URL's hostname instead of the listen address.
			// The TLS certificate's SAN must match the hostname used by clients.
			// Example: server listens on 0.0.0.0:18888 but certificate is valid for dev.hoop.dev:PORT
			if apiURL, err := url.Parse(appconfig.Get().ApiURL()); err == nil && apiURL.Hostname() != "" {
				host = apiURL.Hostname()
			}
		}
		baseCommand := fmt.Sprintf("%s://%s:%s/", scheme, host, serverPort)
		curlCommand := fmt.Sprintf("curl -H 'Authorization: %s' %s", secretKey, baseCommand)
		browserCommand := fmt.Sprintf("%s%s", baseCommand, secretKey)

		jsonCommandsString := `{
				"curl": "` + curlCommand + `",
				"browser": "` + browserCommand + `"
			}`
		base.ConnectionType = proto.ConnectionType(connectionType).String()
		base.ConnectionCredentials = &openapi.HttpProxyConnectionInfo{
			Hostname:   host,
			Port:       serverPort,
			ProxyToken: secretKey,
			Command:    jsonCommandsString,
		}
	default:
		return nil
	}

	return &base
}

func isConnectionTypeConfigured(connType proto.ConnectionType) bool {
	if connType == proto.ConnectionTypeSSM {
		return true // Same API router so always configured
	}

	serverConf, err := models.GetServerMiscConfig()
	if err != nil || serverConf == nil {
		return false
	}

	switch connType {
	case proto.ConnectionTypePostgres:
		return serverConf.PostgresServerConfig != nil && serverConf.PostgresServerConfig.ListenAddress != ""
	case proto.ConnectionTypeSSH:
		return serverConf.SSHServerConfig != nil && serverConf.SSHServerConfig.ListenAddress != ""
	case proto.ConnectionTypeRDP:
		return serverConf.RDPServerConfig != nil && serverConf.RDPServerConfig.ListenAddress != ""
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes, proto.ConnectionTypeClaudeCode:
		return serverConf.HttpProxyServerConfig != nil && serverConf.HttpProxyServerConfig.ListenAddress != ""
	default:
		return false
	}
}

func getServerHostAndPort(serverConf *models.ServerMiscConfig, connType proto.ConnectionType) (host, portNumber string) {
	var listenAddr string
	switch connType {
	case proto.ConnectionTypePostgres:
		if serverConf != nil && serverConf.PostgresServerConfig != nil {
			listenAddr = serverConf.PostgresServerConfig.ListenAddress
		}
	case proto.ConnectionTypeSSH:
		if serverConf != nil && serverConf.SSHServerConfig != nil {
			listenAddr = serverConf.SSHServerConfig.ListenAddress
		}
	case proto.ConnectionTypeRDP:
		if serverConf != nil && serverConf.RDPServerConfig != nil {
			listenAddr = serverConf.RDPServerConfig.ListenAddress
		}
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes:
		if serverConf != nil && serverConf.HttpProxyServerConfig != nil {
			listenAddr = serverConf.HttpProxyServerConfig.ListenAddress
		}
	}

	host, portNumber, _ = strings.Cut(listenAddr, ":")
	if host == "localhost" {
		host = "127.0.0.1"
	}

	return
}

// createConnectionCredentialsReview creates a review record for connection credentials access
func createConnectionCredentialsReview(ctx *storagev2.Context, conn *models.Connection, accessRule *models.AccessRequestRule, sessionID string, accessDurationSec int) (string, error) {
	// Get user info for slack_id
	user, err := models.GetUserByEmail(ctx.UserEmail)
	if err != nil {
		return "", fmt.Errorf("failed fetching user: %w", err)
	}
	if user == nil {
		return "", fmt.Errorf("user %s not found", ctx.UserEmail)
	}

	// Determine reviewers: use access rule if available, otherwise use connection reviewers
	var reviewerGroups []string
	if accessRule != nil && len(accessRule.ReviewersGroups) > 0 {
		reviewerGroups = accessRule.ReviewersGroups
	} else if len(conn.Reviewers) > 0 {
		reviewerGroups = conn.Reviewers
	} else {
		return "", fmt.Errorf("no reviewers configured for connection")
	}

	// Create review groups
	var reviewGroups []models.ReviewGroups
	for _, groupName := range reviewerGroups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     ctx.OrgID,
			GroupName: groupName,
			Status:    models.ReviewStatusPending,
		})
	}

	// Create review record - always JIT type for credentials
	accessDuration := time.Duration(accessDurationSec) * time.Second
	reviewID := uuid.NewString()

	newRev := &models.Review{
		ID:                reviewID,
		OrgID:             ctx.OrgID,
		Type:              models.ReviewTypeJit,
		SessionID:         sessionID,
		ConnectionName:    conn.Name,
		ConnectionID:      sql.NullString{String: conn.ID, Valid: true},
		AccessDurationSec: int64(accessDuration.Seconds()),
		InputEnvVars:      nil, // Credentials don't have env vars
		InputClientArgs:   nil, // Credentials don't have client args
		OwnerID:           ctx.UserID,
		OwnerEmail:        ctx.UserEmail,
		OwnerName:         &ctx.UserName,
		OwnerSlackID:      &user.SlackID,
		Status:            models.ReviewStatusPending,
		ReviewGroups:      reviewGroups,
		CreatedAt:         time.Now().UTC(),
		RevokedAt:         nil,
	}

	log.With("sid", sessionID, "id", newRev.ID, "user", ctx.UserID, "org", ctx.OrgID,
		"type", models.ReviewTypeJit, "duration", fmt.Sprintf("%vs", accessDuration.Seconds())).
		Infof("creating review for connection credentials")

	// Create review with empty session input (credentials don't have input)
	if err := models.CreateReview(newRev, ""); err != nil {
		return "", fmt.Errorf("failed saving review: %w", err)
	}

	return reviewID, nil
}

// checkConnectionRequiresReview checks if a connection requires review/JIT approval
// It checks both OSS reviewers and Enterprise access request rules
func checkConnectionRequiresReview(ctx *storagev2.Context, conn *models.Connection) (bool, *models.AccessRequestRule) {
	// Check OSS reviewers
	if len(conn.Reviewers) > 0 {
		return true, nil
	}

	// Check Enterprise access request rules for JIT access
	orgID, err := uuid.Parse(ctx.OrgID)
	if err != nil {
		log.Warnf("failed parsing org_id %s, err=%v", ctx.OrgID, err)
		return false, nil
	}

	accessRule, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, conn.Name, "jit")
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		log.Warnf("failed checking access request rules for connection %s, err=%v", conn.Name, err)
		return false, nil
	}

	if accessRule != nil {
		return true, accessRule
	}

	return false, nil
}

func generateSecretKey(connType proto.ConnectionType) (string, string, error) {
	const keySize = 32

	switch connType {
	case proto.ConnectionTypePostgres:
		return keys.GenerateSecureRandomKey("pg", keySize)
	case proto.ConnectionTypeSSH:
		return keys.GenerateSecureRandomKey("ssh", keySize)
	case proto.ConnectionTypeRDP:
		return keys.GenerateSecureRandomKey("rdp", keySize)
	case proto.ConnectionTypeSSM:
		return keys.GenerateSecureRandomKey("aws-ssm", keySize)
	case proto.ConnectionTypeHttpProxy:
		return keys.GenerateSecureRandomKey("httpproxy", keySize)
	case proto.ConnectionTypeClaudeCode:
		return keys.GenerateSecureRandomKey("claude-code", keySize)
	case proto.ConnectionTypeKubernetes:
		return keys.GenerateSecureRandomKey("k8s", keySize)
	default:
		return "", "", fmt.Errorf("unsupported connection type %v", connType)
	}
}
