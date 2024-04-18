package apiorgs

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	apiagents "github.com/runopsio/hoop/gateway/api/agents"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
)

var agentKeyDefaultName = "_default"

func CreateAgentKey(c *gin.Context) {
	ctx := user.ContextUser(c)
	secretKey, secretKeyHash, err := dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		log.Errorf("failed generating organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating agent token"})
		return
	}
	ag, err := pgagents.New().FetchOneByNameOrID(ctx, agentKeyDefaultName)
	if err != nil {
		log.Errorf("failed fetching for existing organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ag != nil {
		log.Infof("agent (org token) %v already exists", agentKeyDefaultName)
		c.JSON(http.StatusConflict, gin.H{"message": "organization token already exists"})
		return
	}
	ag = &pgrest.Agent{
		ID:       apiagents.DeterministicAgentUUID(ctx.GetOrgID(), agentKeyDefaultName),
		OrgID:    ctx.GetOrgID(),
		Name:     agentKeyDefaultName,
		Mode:     proto.AgentModeMultiConnectionType,
		KeyHash:  secretKeyHash, // TODO: change to token hash
		Key:      secretKey,
		Status:   pgrest.AgentStatusDisconnected,
		Metadata: map[string]string{},
	}
	grpcURL := storagev2.ParseContext(c).GrpcURL
	dsn, err := dsnkeys.New(grpcURL, agentKeyDefaultName, secretKey)
	if err != nil {
		log.Errorf("failed generating agent key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}
	if err := pgagents.New().Upsert(ag); err != nil {
		log.Errorf("failed generating organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating agent token"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":  ag.ID,
		"key": dsn,
	})
}

func GetAgentKey(c *gin.Context) {
	ctx := user.ContextUser(c)
	ag, err := pgagents.New().FetchOneByNameOrID(ctx, agentKeyDefaultName)
	if err != nil {
		log.Errorf("failed fetching for existing organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ag == nil {
		log.Infof("%v already exists", agentKeyDefaultName)
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
	ctx := user.ContextUser(c)
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
