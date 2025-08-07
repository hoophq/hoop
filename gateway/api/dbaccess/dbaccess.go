package apidbaccess

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

type DbAccessRequest struct {
	ConnectionName    string `json:"connection_name"`
	AccessDurationSec int    `json:"access_duration_seconds"`
}

func CreateDbAccess(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req DbAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
		return
	}
	conn, err := models.GetConnectionByNameOrID(ctx, req.ConnectionName)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.AbortWithStatusJSON(404, gin.H{"message": "connection not found"})
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
	secretKey := uuid.NewString()
	db, err := models.CreateDbAccess(&models.DbAccess{
		ID:             uuid.NewString(),
		OrgID:          ctx.OrgID,
		UserID:         ctx.UserID,
		ConnectionName: conn.Name,
		DbName:         defaultDB,
		DbHostname:     dbHostname,
		DbUsername:     secretKey,
		DbPassword:     "none",
		DbPort:         "15432",
		Status:         "active",
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       time.Now().UTC().Add(time.Duration(req.AccessDurationSec) * time.Second),
	})
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	connectionString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		db.DbUsername, db.DbPassword, db.DbHostname, db.DbPort, defaultDB)
	c.JSON(201, gin.H{
		"id":                db.ID,
		"user_id":           db.UserID,
		"connection_name":   db.ConnectionName,
		"database_name":     db.DbName,
		"hostname":          db.DbHostname,
		"username":          db.DbUsername,
		"password":          db.DbPassword,
		"port":              db.DbPort,
		"status":            db.Status,
		"created_at":        db.CreatedAt.Format(time.RFC3339),
		"expire_at":         db.ExpireAt.Format(time.RFC3339),
		"connection_string": connectionString,
	})
}

func GetDbAccessByID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	resourceID := c.Param("id")
	db, err := models.GetDbAccessByID(ctx.OrgID, resourceID)
	switch err {
	case models.ErrNotFound:
		c.AbortWithStatusJSON(404, gin.H{"message": err.Error()})
	case nil:
		connectionString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			db.DbUsername, db.DbPassword, db.DbHostname, db.DbPort, db.DbName)
		c.JSON(200, gin.H{
			"id":                db.ID,
			"user_id":           db.UserID,
			"connection_name":   db.ConnectionName,
			"database_name":     db.DbName,
			"hostname":          db.DbHostname,
			"username":          db.DbUsername,
			"password":          db.DbPassword,
			"port":              db.DbPort,
			"status":            db.Status,
			"created_at":        db.CreatedAt.Format(time.RFC3339),
			"expire_at":         db.ExpireAt.Format(time.RFC3339),
			"connection_string": connectionString,
		})
	default:
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
	}
}
