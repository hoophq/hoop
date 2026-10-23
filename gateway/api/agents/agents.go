package apiagents

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/httputils"
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
	if err := apivalidation.ValidateTags("tags", req.Tags); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey("", 32)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating agent token")
		return
	}

	if req.Mode != proto.AgentModeEmbeddedType && req.Mode != proto.AgentModeStandardType {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("unknown agent mode %q", req.Mode)})
		return
	}
	dsn, err := dsnkeys.NewString(storagev2.ParseContext(c).GrpcURL, req.Name, secretKey, req.Mode)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating dsn")
		return
	}

	err = models.CreateAgentWithTags(models.DB, ctx.OrgID, req.Name, req.Mode, secretKeyHash, req.Tags)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": models.ErrAlreadyExists.Error()})
	case nil:
		c.JSON(http.StatusCreated, openapi.AgentCreateResponse{Token: dsn})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating agent resource: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed removing agent resource: %v", err)
		return
	}
}

// GetAgent
//
//	@Summary		Get Agent Key
//	@Description	Get an agent key by name or ID
//	@Tags			Agents
//	@Produce		json
//	@Param			nameOrID	path		string	true	"The name or ID of the resource"
//	@Success		200			{object}	openapi.AgentResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/agents/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	nameOrID := c.Param("nameOrID")
	agent, err := models.GetAgentByNameOrID(ctx.OrgID, nameOrID)

	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "agent not found"})
			return
		}

		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting agent: %v", err)
		return
	}

	mode := agent.Mode
	if mode == "" {
		// set to default mode if the entity doesn't contain any value
		mode = proto.AgentModeStandardType
	}

	if mode == proto.AgentModeMultiConnectionType {
		// for now, don't return multi-connection keys
		// there's a special route for managing these kind of token.
		// See orgs/orgs.go
		c.JSON(http.StatusNotFound, gin.H{"message": "agent not found"})
		return
	}

	tags, err := models.GetAgentTags(models.DB, ctx.OrgID, agent.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting agent tags: %v", err)
		return
	}
	c.JSON(http.StatusOK, toAgentResponse(*agent, tags))
}

// ListAgents
//
//	@Summary		List Agent Keys
//	@Description	List all agent keys
//	@Tags			Agents
//	@Produce		json
//	@Param			status			query		string	false	"Filter by status (CONNECTED or DISCONNECTED)"
//	@Param			tag_selector	query		string	false	"Selector tags to filter on, supports '=' and '!=' (e.g. key1=value1,key2!=value2)"	Format(string)
//	@Success		200				{array}		openapi.AgentResponse
//	@Failure		400,500			{object}	openapi.HTTPError
//	@Router			/agents [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	opts := models.AgentFilterOption{
		Status:      c.Query("status"),
		TagSelector: c.Query("tag_selector"),
	}
	if err := models.ValidateTagSelector(opts.TagSelector); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	items, err := models.ListAgents(models.DB, ctx.OrgID, opts)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing agents")
		return
	}
	tagsByAgent, err := models.ListAgentTags(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing agent tags")
		return
	}
	result := []openapi.AgentResponse{}
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
		result = append(result, toAgentResponse(a, tagsByAgent[a.ID]))
	}
	c.JSON(http.StatusOK, result)
}

// toAgentResponse returns tags as an empty object, never null, when the agent
// has none.
func toAgentResponse(a models.Agent, tags map[string]string) openapi.AgentResponse {
	if tags == nil {
		tags = map[string]string{}
	}
	return openapi.AgentResponse{
		ID:       a.ID,
		Token:    "", // don't show the hashed token
		Name:     a.Name,
		Mode:     a.Mode,
		Status:   a.Status,
		Metadata: a.Metadata,
		Tags:     tags,
		// DEPRECATE top level metadata keys
		Hostname:      a.Metadata["hostname"],
		MachineID:     a.Metadata["machine_id"],
		KernelVersion: a.Metadata["kernel_version"],
		Version:       a.Metadata["version"],
		GoVersion:     a.Metadata["goversion"],
		Compiler:      a.Metadata["compiler"],
		Platform:      a.Metadata["platform"],
	}
}
