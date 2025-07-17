package apiagents

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
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
//	@Tags			Agents
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.AgentRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.AgentCreateResponse
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/agents [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	req := openapi.AgentRequest{Mode: proto.AgentModeStandardType}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Infof("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey("", 32)
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

	err = models.CreateAgent(ctx.OrgID, req.Name, req.Mode, secretKeyHash)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": models.ErrAlreadyExists.Error()})
	case nil:
		c.JSON(http.StatusCreated, openapi.AgentCreateResponse{Token: dsn})
	default:
		log.Errorf("failed creating agent resource, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

// DeleteAgent
//
//	@Summary		Delete Agent Key
//	@Description	Remove an agent key. It will invalidate a running agent
//	@Tags			Agents
//	@Produce		json
//	@Param			nameOrID	path	string	true	"The name or ID of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/agents/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	nameOrID := c.Param("nameOrID")
	err := models.DeleteAgentByNameOrID(ctx.OrgID, nameOrID)
	switch err {
	case nil:
		c.Writer.WriteHeader(204)
	default:
		log.Errorf("failed removing agent resource %v, err=%#v", nameOrID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

// ListAgents
//
//	@Summary		List Agent Keys
//	@Description	List all agent keys
//	@Tags			Agents
//	@Produce		json
//	@Success		200	{array}		openapi.AgentListResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agents [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAgents(ctx.OrgID)
	if err != nil {
		log.Errorf("failed listing agents, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing agents"})
		return
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
			Hostname:      a.Metadata["hostname"],
			MachineID:     a.Metadata["machine_id"],
			KernelVersion: a.Metadata["kernel_version"],
			Version:       a.Metadata["version"],
			GoVersion:     a.Metadata["goversion"],
			Compiler:      a.Metadata["compiler"],
			Platform:      a.Metadata["platform"],
		})
	}
	c.JSON(http.StatusOK, result)
}
