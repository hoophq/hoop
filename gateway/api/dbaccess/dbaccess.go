package apidbaccess

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// CreateConnectionDbAccess
//
//	@Summary		Create Database Access
//	@Description	Create Database Access
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string								true	"Name or UUID of the connection"
//	@Param			request		body		openapi.ConnectionDbAccessRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.ConnectionDbAccess
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/dbaccess [post]
func CreateConnectionDbAccess(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ConnectionDbAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}

	serverConfig, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		c.AbortWithStatusJSON(500, gin.H{"message": fmt.Sprintf("failed to retrieve server config, err=%v", err)})
		return
	}

	var pgServerListenAddr string
	if serverConfig != nil && serverConfig.PostgresServerConfig != nil {
		pgServerListenAddr = serverConfig.PostgresServerConfig.ListenAddress
	}

	_, listenPort, _ := strings.Cut(pgServerListenAddr, ":")
	if listenPort == "" {
		c.AbortWithStatusJSON(400, gin.H{"message": "postgres server proxy is not configured"})
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

	if conn.SubType.String != string(proto.ConnectionTypePostgres) {
		c.AbortWithStatusJSON(400, gin.H{"message": "unsupported connection type"})
		return
	}

	if conn.AccessModeConnect != "enabled" {
		c.AbortWithStatusJSON(400, gin.H{"message": "access mode connect is not enabled for this connection"})
		return
	}

	dbHostname := appconfig.Get().ApiHostname()
	if dbHostname == "localhost" {
		dbHostname = "127.0.0.1"
	}

	defaultDB := "postgres"
	defaultDBEnc := conn.Envs["envvar:DB"]
	if defaultDBEnc != "" {
		defaultDBBytes, _ := base64.RawStdEncoding.DecodeString(defaultDBEnc)
		defaultDB = string(defaultDBBytes)
	}
	secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey("pgaccess", 32)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": "failed to generate secret key"})
		return
	}
	expireAt := time.Now().UTC().Add(time.Duration(req.AccessDurationSec) * time.Second)
	if expireAt.After(time.Now().UTC().Add(48 * time.Hour)) {
		c.AbortWithStatusJSON(400, gin.H{"message": "access duration cannot exceed 48 hours"})
		return
	}
	db, err := models.CreateConnectionDbAccess(&models.DbAccess{
		ID:             uuid.NewString(),
		OrgID:          ctx.OrgID,
		UserSubject:    ctx.UserID,
		ConnectionName: conn.Name,
		SecretKeyHash:  secretKeyHash,
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       expireAt,
	})
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	dbPassword, dbPort := "noop", listenPort
	connectionString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		secretKey, dbPassword, dbHostname, dbPort, defaultDB)
	c.JSON(201,
		openapi.ConnectionDbAccess{
			ID:               db.ID,
			DatabaseName:     defaultDB,
			Hostname:         dbHostname,
			Username:         secretKey,
			Password:         dbPassword,
			Port:             dbPort,
			ConnectionString: connectionString,
			CreatedAt:        db.CreatedAt,
			ExpireAt:         db.ExpireAt,
		},
	)
}
