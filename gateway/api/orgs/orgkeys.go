package apiorgs

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const agentKeyDefaultName string = "_default"

var (
	ErrAlreadyExists = errors.New("org key already exists")
)

// CreateOrgKey
//
//	@Summary		Create Org Key
//	@Description	Create the organization key to run with `hoop run` command line.
//	@Tags			Organization Management
//	@Produce		json
//	@Success		201		{object}	openapi.OrgKeyResponse
//	@Failure		409,500	{object}	openapi.HTTPError
//	@Router			/orgs/keys [post]
func CreateAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	evt := audit.NewEvent(audit.ResourceOrgKey, audit.ActionCreate).
		Resource("", agentKeyDefaultName)
	defer func() { evt.Log(c) }()

	agentID, dsn, err := ProvisionOrgAgentKey(ctx.OrgID, storagev2.ParseContext(c).GrpcURL)
	evt.Resource(agentID, agentKeyDefaultName).Err(err)
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
	c.JSON(http.StatusCreated, openapi.OrgKeyResponse{
		ID:  agentID,
		Key: dsn,
	})
}

// GetOrgKey
//
//	@Summary		Get Org Key
//	@Description	Get the organization key to run with `hoop run` command line
//	@Tags			Organization Management
//	@Produce		json
//	@Success		200		{object}	openapi.OrgKeyResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/orgs/keys [get]
func GetAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ag, err := models.GetAgentByNameOrID(ctx.OrgID, agentKeyDefaultName)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "organization token not found"})
		return
	case nil:
	default:
		log.Errorf("failed fetching for existing organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	dsn, err := dsnkeys.New(ctx.GrpcURL, agentKeyDefaultName, ag.Key)
	if err != nil {
		log.Errorf("failed generating agent key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}
	c.JSON(http.StatusOK, openapi.OrgKeyResponse{
		ID:  ag.ID,
		Key: dsn,
	})
}

// RevokeAgentKey
//
//	@Summary		Revoke Org Key
//	@Description	Remove organization key
//	@Tags			Organization Management
//	@Produce		json
//	@Success		204
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/keys [delete]
func RevokeAgentKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	_, err := models.GetAgentByNameOrID(ctx.OrgID, agentKeyDefaultName)
	switch err {
	case models.ErrNotFound:
		c.Writer.WriteHeader(204)
		return
	case nil:
		evt := audit.NewEvent(audit.ResourceOrgKey, audit.ActionRevoke).
			Resource(agentKeyDefaultName, agentKeyDefaultName)
		defer func() { evt.Log(c) }()

		delErr := models.DeleteAgentByNameOrID(ctx.OrgID, agentKeyDefaultName)
		evt.Err(delErr)
		if delErr != nil {
			log.Errorf("failed removing organization token for %v, err=%v", agentKeyDefaultName, delErr)
			c.JSON(http.StatusInternalServerError, gin.H{"message": delErr.Error()})
			return
		}
	default:
		audit.NewEvent(audit.ResourceOrgKey, audit.ActionRevoke).
			Resource(agentKeyDefaultName, agentKeyDefaultName).
			Err(err).
			Log(c)
		log.Errorf("failed fetching for existing organization token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.Writer.WriteHeader(204)
}

func ProvisionOrgAgentKey(orgID, grpcURL string) (agentID, dsn string, err error) {
	_, err = models.GetAgentByNameOrID(orgID, agentKeyDefaultName)
	switch err {
	case models.ErrNotFound:
	case nil:
		return "", "", ErrAlreadyExists
	default:
		return "", "", fmt.Errorf("failed fetching for existing organization token, err=%v", err)
	}
	secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey("", 32)
	if err != nil {
		return "", "", fmt.Errorf("failed generating organization token: %v", err)
	}
	dsn, err = dsnkeys.New(grpcURL, agentKeyDefaultName, secretKey)
	if err != nil {
		return "", "", fmt.Errorf("failed generating agent key: %v", err)
	}
	err = models.CreateAgentOrgKey(
		orgID,
		agentKeyDefaultName,
		proto.AgentModeMultiConnectionType,
		secretKey,
		secretKeyHash)
	if err != nil {
		return "", "", fmt.Errorf("failed generating organization token: %v", err)
	}
	return
}
