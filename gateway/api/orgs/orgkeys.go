package apiorgs

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	apiagents "github.com/hoophq/hoop/gateway/api/agents"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgagents "github.com/hoophq/hoop/gateway/pgrest/agents"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var (
	agentKeyDefaultName = "_default"
	ErrAlreadyExists    = errors.New("org key already exists")
)

func CreateAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	agentID, dsn, err := ProvisionOrgAgentKey(ctx, storagev2.ParseContext(c).GrpcURL)
	switch err {
	case ErrAlreadyExists:
		log.Infof("agent (org token) %v already exists", agentKeyDefaultName)
		c.JSON(http.StatusConflict, gin.H{"message": "organization token already exists"})
		return
	case nil: // noop
	default:
		log.Errorf("failed provisioning org agent key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":  agentID,
		"key": dsn,
	})
}

func GetAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ag, err := pgagents.New().FetchOneByNameOrID(ctx, agentKeyDefaultName)
	if err != nil {
		log.Errorf("failed fetching for existing organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ag == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "organization token not found"})
		return
	}
	grpcURL := storagev2.ParseContext(c).GrpcURL
	dsn, err := dsnkeys.New(grpcURL, agentKeyDefaultName, ag.Key)
	if err != nil {
		log.Errorf("failed generating agent key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":  ag.ID,
		"key": dsn,
	})
}

func RevokeAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ag, err := pgagents.New().FetchOneByNameOrID(ctx, agentKeyDefaultName)
	if err != nil {
		log.Errorf("failed fetching organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ag == nil {
		c.Writer.WriteHeader(204)
		return
	}
	if err := pgagents.New().Delete(ctx, ag.ID); err != nil {
		log.Errorf("failed removing organization token for %v, err=%v", agentKeyDefaultName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.Writer.WriteHeader(204)
}

func ProvisionOrgAgentKey(ctx pgrest.OrgContext, grpcURL string) (agentID, dsn string, err error) {
	ag, err := pgagents.New().FetchOneByNameOrID(ctx, agentKeyDefaultName)
	if err != nil {
		return "", "", fmt.Errorf("failed fetching for existing organization token, err=%v", err)
	}
	if ag != nil {
		return "", "", ErrAlreadyExists
	}
	secretKey, secretKeyHash, err := dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		return "", "", fmt.Errorf("failed generating organization token: %v", err)
	}
	agentID = apiagents.DeterministicAgentUUID(ctx.GetOrgID(), agentKeyDefaultName)
	ag = &pgrest.Agent{
		ID:       agentID,
		OrgID:    ctx.GetOrgID(),
		Name:     agentKeyDefaultName,
		Mode:     proto.AgentModeMultiConnectionType,
		KeyHash:  secretKeyHash, // TODO: change to token hash
		Key:      secretKey,
		Status:   pgrest.AgentStatusDisconnected,
		Metadata: map[string]string{},
	}
	dsn, err = dsnkeys.New(grpcURL, agentKeyDefaultName, secretKey)
	if err != nil {
		return "", "", fmt.Errorf("failed generating agent key: %v", err)
	}
	if err := pgagents.New().Upsert(ag); err != nil {
		return "", "", fmt.Errorf("failed generating organization token: %v", err)
	}
	return
}
