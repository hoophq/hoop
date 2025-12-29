package apiconnections

import (
	"encoding/base64"
	"fmt"
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
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/ssmproxy"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var validConnectionTypes = []string{"postgres", "ssh", "rdp", "aws-ssm", "httpproxy"}

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

	if len(conn.Reviewers) > 0 {
		c.AbortWithStatusJSON(400, gin.H{"message": "connection reviewers are not supported for this connection"})
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
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       expireAt,
	})
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	c.JSON(201, buildConnectionCredentialsResponse(db, conn, serverConf, secretKey))
}

func buildConnectionCredentialsResponse(
	cred *models.ConnectionCredentials,
	conn *models.Connection,
	serverConf *models.ServerMiscConfig,
	secretKey string) *openapi.ConnectionCredentialsResponse {
	const dummyString = "hoop"

	base := openapi.ConnectionCredentialsResponse{
		ID:             cred.ID,
		ConnectionType: cred.ConnectionType,
		ConnectionName: cred.ConnectionName,
		CreatedAt:      cred.CreatedAt,
		ExpireAt:       cred.ExpireAt,
	}

	connectionType := proto.ConnectionType(cred.ConnectionType)
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

		endpoint := fmt.Sprintf("%s/ssm", appconfig.Get().ApiURL())
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
	case proto.ConnectionTypeHttpProxy:
		scheme := "http"
		if appconfig.Get().GatewayTLSKey() != "" {
			scheme = "https"
		}
		baseCommand := fmt.Sprintf("%s://%s:%s/", scheme, serverHost, serverPort)
		curlCommand := fmt.Sprintf("curl -H 'Authorization: %s' %s", secretKey, baseCommand)
		browserCommand := fmt.Sprintf("%s%s", baseCommand, secretKey)

		base.ConnectionCredentials = &openapi.HttpProxyConnectionInfo{
			Hostname:   serverHost,
			Port:       serverPort,
			ProxyToken: secretKey,
			Command:    fmt.Sprintf("cURL: %s\n Browser: %s", curlCommand, browserCommand),
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
	case proto.ConnectionTypeHttpProxy:
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
	case proto.ConnectionTypeHttpProxy:
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
	default:
		return "", "", fmt.Errorf("unsupported connection type %v", connType)
	}
}
