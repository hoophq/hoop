package apiconnections

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/httpproxy"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// CreateConnectionCredentials
//
//	@Summary		Create Connection Credentials
//	@Description	Create Connection Credentials
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string									true	"Name or UUID of the connection"
//	@Param			request		body		openapi.ConnectionCredentialsRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.ConnectionCredentials
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

	if conn.AccessModeConnect != "enabled" {
		c.AbortWithStatusJSON(400, gin.H{"message": "access mode connect is not enabled for this connection"})
		return
	}

	serverHostname := appconfig.Get().ApiHostname()
	if serverHostname == "localhost" {
		serverHostname = "127.0.0.1"
	}

	cred, err := newAccessCredentials(serverHostname, serverConf, conn)
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
		SecretKeyHash:  cred.secretKeyHash,
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       expireAt,
	})
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	c.JSON(201,
		openapi.ConnectionCredentials{
			ID:               db.ID,
			DatabaseName:     ptr.ToString(cred.databaseName),
			Hostname:         cred.serverHostname,
			Username:         cred.username,
			Password:         cred.secretKey,
			Port:             cred.serverPort,
			ConnectionString: cred.connectionString,
			CreatedAt:        db.CreatedAt,
			ExpireAt:         db.ExpireAt,
		},
	)
}

type credentialsInfo struct {
	serverHostname   string
	serverPort       string
	username         string
	secretKey        string
	secretKeyHash    string
	databaseName     *string
	connectionString string
}

func getKeyPrefixAndServerPort(serverConf *models.ServerMiscConfig, connType proto.ConnectionType) (keyPrefix, portNumber string) {
	var listenAddr string
	switch connType {
	case proto.ConnectionTypePostgres:
		if serverConf != nil && serverConf.PostgresServerConfig != nil {
			listenAddr = serverConf.PostgresServerConfig.ListenAddress
		}
		keyPrefix = "pg"
	case proto.ConnectionTypeSSH:
		if serverConf != nil && serverConf.SSHServerConfig != nil {
			listenAddr = serverConf.SSHServerConfig.ListenAddress
		}
		keyPrefix = "ssh"
	case proto.ConnectionTypeHttpProxy:
		if serverConf != nil && serverConf.HTTPServerConfig != nil {
			listenAddr = serverConf.HTTPServerConfig.ListenAddress
		}
		keyPrefix = "http"
	}
	_, portNumber, _ = strings.Cut(listenAddr, ":")
	return
}

func newAccessCredentials(serverHost string, serverConf *models.ServerMiscConfig, conn *models.Connection) (credentialsInfo, error) {
	connType := proto.ConnectionType(conn.SubType.String)

	keyPrefix, serverPort := getKeyPrefixAndServerPort(serverConf, connType)
	secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey(keyPrefix, 32)
	if err != nil {
		return credentialsInfo{}, fmt.Errorf("failed to generate credentials: %w", err)
	}
	info := credentialsInfo{
		serverHostname:   serverHost,
		serverPort:       serverPort,
		username:         "hoop",
		secretKey:        secretKey,
		secretKeyHash:    secretKeyHash,
		databaseName:     nil,
		connectionString: "",
	}

	defaultDBEnc := conn.Envs["envvar:DB"]
	if defaultDBEnc != "" {
		defaultDBBytes, _ := base64.RawStdEncoding.DecodeString(defaultDBEnc)
		info.databaseName = ptr.String(string(defaultDBBytes))
	}
	switch connType {
	case proto.ConnectionTypePostgres:
		if serverConf == nil || serverConf.PostgresServerConfig == nil {
			return credentialsInfo{}, fmt.Errorf("server proxy is not configured for Postgres")
		}
		if info.databaseName == nil {
			info.databaseName = ptr.String("postgres")
		}
		info.connectionString = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			secretKey, info.username, serverHost, serverPort, ptr.ToString(info.databaseName))
	case proto.ConnectionTypeSSH:
		if serverConf == nil || serverConf.SSHServerConfig == nil {
			return credentialsInfo{}, fmt.Errorf("server proxy is not configured for SSH")
		}
		info.connectionString = fmt.Sprintf("ssh://%s@%s:%s", info.username, info.serverHostname, info.serverPort)
	case proto.ConnectionTypeHttpProxy:
		if serverConf == nil || serverConf.HTTPServerConfig == nil {
			return credentialsInfo{}, fmt.Errorf("server proxy is not configured for HTTP")
		}
		info.username = ""
		info.connectionString = fmt.Sprintf("http://%s:%s?%s=%s", info.serverHostname, info.serverPort, httpproxy.HoopSecretQuery, secretKey)
	default:
		return credentialsInfo{}, fmt.Errorf("unsupported connection type %v", connType)
	}
	return info, nil
}
