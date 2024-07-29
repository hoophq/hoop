package apiagents

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgagents "github.com/hoophq/hoop/gateway/pgrest/agents"
	"github.com/hoophq/hoop/gateway/storagev2"
)

type AgentRequest struct {
	Name string `json:"name" binding:"required"`
	Mode string `json:"mode"`
}

// CreateAgent
//
//	@Summary		Create Agent Key
//	@Description	Create an agent key in a DSN format: `grpc(s)://<name>:<key>@<grpc-host>:<grpc-port>?mode=standard|embedded`.
//	@Description	This key is used to deploy agents and expose internal resources from your infra-structure
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.AgentRequest	true	"The request body resource"
//	@Success		201		{object}	openapi.AgentCreateResponse
//	@Failure		400		{object}	openapi.HTTPError
//	@Failure		409		{object}	openapi.HTTPError
//	@Failure		422		{object}	openapi.HTTPError
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/agents [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	req := openapi.AgentRequest{Mode: proto.AgentModeStandardType}
	// req := AgentRequest{Mode: proto.AgentModeStandardType}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Infof("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	existentAgent, err := pgagents.New().FetchOneByNameOrID(ctx, req.Name)
	if err != nil {
		log.Errorf("failed validating agent, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if existentAgent != nil {
		log.Infof("agent %v already exists", req.Name)
		c.JSON(http.StatusConflict, gin.H{"message": "agent already exists"})
		return
	}

	secretKey, secretKeyHash, err := dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		log.Errorf("failed generating agent token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating agent token"})
		return
	}

	if req.Mode != proto.AgentModeEmbeddedType && req.Mode != proto.AgentModeStandardType {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("unknown agent mode %q", req.Mode)})
		return
	}
	dsn, err := dsnkeys.NewString(storagev2.ParseContext(c).GrpcURL, req.Name, secretKey, req.Mode)
	if err != nil {
		log.Errorf("failed generating dsn, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}

	err = pgagents.New().Upsert(&pgrest.Agent{
		// a deterministic uuid allows automatic reasign of resources
		// in case of removal and creating with the same name (e.g. connections)
		ID:       DeterministicAgentUUID(ctx.GetOrgID(), req.Name),
		Name:     req.Name,
		OrgID:    ctx.GetOrgID(),
		KeyHash:  secretKeyHash,
		Mode:     req.Mode,
		Status:   pgrest.AgentStatusDisconnected,
		Metadata: map[string]string{},
	})
	if err != nil {
		log.Errorf("failed persisting agent token, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, openapi.AgentCreateResponse{Token: dsn})
}

// DeleteAgent
//
//	@Summary		Delete Agent Key
//	@Description	Remove an agent key. It will invalidate a running agent
//	@Tags			Core
//	@Produce		json
//	@Param			nameOrID	path	string	true	"The name or ID of the resource"
//	@Success		204
//	@Failure		404	{object}	openapi.HTTPError
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agents/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	nameOrID := c.Param("nameOrID")
	agent, err := pgagents.New().FetchOneByNameOrID(ctx, nameOrID)
	if err != nil {
		log.Errorf("failed fetching agent, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if agent == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	if err := pgagents.New().Delete(ctx, agent.ID); err != nil {
		log.Errorf("failed evicting agent %v, err=%v", agent.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.Writer.WriteHeader(204)
}

// ListAgents
//
//	@Summary		List Agent Keys
//	@Description	List all agent keys
//	@Tags			Core
//	@Produce		json
//	@Success		200	{array}		openapi.AgentListResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agents [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	// items, err := pgagents.New().FindAll(context, pgrest.WithEqFilter(c.Request.URL.Query()))
	items, err := pgagents.New().FindAll(ctx)
	if err != nil {
		log.Errorf("failed listing agents, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing agents"})
	}
	result := []openapi.AgentListResponse{}
	for _, a := range items {
		switch a.Mode {
		case proto.AgentModeMultiConnectionType:
			// for now, skip listing multi-connection keys
			// there's a special route for managing these kind of token.
			// See orgs/orgs.go
			continue
		case "":
			// set to default mode if the entity doesn't contain any value
			a.Mode = proto.AgentModeStandardType
		}
		result = append(result, openapi.AgentListResponse{
			ID:       a.ID,
			Token:    "", // don't show the hashed token
			Name:     a.Name,
			Mode:     a.Mode,
			Status:   a.Status,
			Metadata: a.Metadata,
			// DEPRECATE top level metadata keys
			Hostname:      a.GetMeta("hostname"),
			MachineID:     a.GetMeta("machine_id"),
			KernelVersion: a.GetMeta("kernel_version"),
			Version:       a.GetMeta("version"),
			GoVersion:     a.GetMeta("goversion"),
			Compiler:      a.GetMeta("compiler"),
			Platform:      a.GetMeta("platform"),
		})
	}
	c.JSON(http.StatusOK, result)
}

func DeterministicAgentUUID(orgID, agentName string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(
		strings.Join([]string{"agent", orgID, agentName}, "/"))).String()
}
