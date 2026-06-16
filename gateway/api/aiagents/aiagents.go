package aiagents

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// List AI Agents
//
//	@Summary		List AI Agents
//	@Description	List all AI Agents for the organization
//	@Tags			AI Agents
//	@Produce		json
//	@Success		200	{array}		openapi.AIAgentResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/ai-agents [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAIAgents(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing ai agents")
		return
	}
	var resp []openapi.AIAgentResponse
	for _, item := range items {
		resp = append(resp, toResponse(item))
	}
	c.JSON(http.StatusOK, resp)
}

// Get AI Agent
//
//	@Summary		Get AI Agent
//	@Description	Get an AI Agent by name or ID
//	@Tags			AI Agents
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the AI Agent"
//	@Success		200			{object}	openapi.AIAgentResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/ai-agents/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetAIAgentByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching ai agent")
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Create AI Agent
//
//	@Summary		Create AI Agent
//	@Description	Generate a new AI Agent. The raw key is returned only once in the response and cannot be retrieved after creation.
//	@Tags			AI Agents
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.AIAgentCreateRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.AIAgentCreateResponse
//	@Failure		400,409,500		{object}	openapi.HTTPError
//	@Router			/ai-agents [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AIAgentCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	rawKey := models.GenerateAIAgent()
	aiAgent := &models.AIAgent{
		OrgID:     ctx.OrgID,
		Name:      req.Name,
		KeyHash:   models.HashAIAgent(rawKey),
		MaskedKey: models.MaskAIAgent(rawKey),
		Status:    "active",
		Groups:    req.Groups,
		CreatedBy: ctx.UserEmail,
	}

	err := models.CreateAIAgent(aiAgent)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "an ai agent with this name already exists"})
	case nil:
		c.JSON(http.StatusCreated, openapi.AIAgentCreateResponse{
			AIAgentResponse: toResponse(*aiAgent),
			Key:             rawKey,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating ai agent")
	}
}

// Update AI Agent
//
//	@Summary		Update AI Agent
//	@Description	Update an AI Agent's name and/or groups. Works for both active and revoked agents.
//	@Tags			AI Agents
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string						true	"Name or UUID of the AI Agent"
//	@Param			request		body		openapi.AIAgentUpdateRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.AIAgentResponse
//	@Failure		400,404,409,500	{object}	openapi.HTTPError
//	@Router			/ai-agents/{nameOrID} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AIAgentUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existing, err := models.GetAIAgentByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching ai agent")
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	name := existing.Name
	if req.Name != nil {
		name = *req.Name
	}
	groups := req.Groups
	if groups == nil {
		groups = existing.Groups
	}

	aiAgent := &models.AIAgent{
		ID:     existing.ID,
		OrgID:  ctx.OrgID,
		Name:   name,
		Groups: groups,
	}

	err = models.UpdateAIAgent(aiAgent)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "an ai agent with this name already exists"})
	case nil:
		updated, err := models.GetAIAgentByNameOrID(ctx.OrgID, existing.ID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching updated ai agent")
			return
		}
		if updated == nil {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		c.JSON(http.StatusOK, toResponse(*updated))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating ai agent")
	}
}

// Reactivate AI Agent
//
//	@Summary		Reactivate AI Agent
//	@Description	Reactivate a revoked AI Agent. Sets status back to active and clears deactivation metadata.
//	@Tags			AI Agents
//	@Produce		json
//	@Param			nameOrID		path		string	true	"Name or UUID of the AI Agent"
//	@Success		200				{object}	openapi.AIAgentResponse
//	@Failure		404,422,500		{object}	openapi.HTTPError
//	@Router			/ai-agents/{nameOrID}/reactivate [post]
func Reactivate(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	existing, err := models.GetAIAgentByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching ai agent")
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}
	if existing.Status == "active" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot reactivate an active ai agent"})
		return
	}

	err = models.ReactivateAIAgent(ctx.OrgID, existing.ID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		updated, err := models.GetAIAgentByNameOrID(ctx.OrgID, existing.ID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching reactivated ai agent")
			return
		}
		if updated == nil {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		c.JSON(http.StatusOK, toResponse(*updated))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reactivating ai agent")
	}
}

// Revoke AI Agent
//
//	@Summary		Revoke AI Agent
//	@Description	Revoke an AI Agent (soft delete). The agent status is set to revoked.
//	@Tags			AI Agents
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the AI Agent"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai-agents/{nameOrID} [delete]
func Revoke(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	existing, err := models.GetAIAgentByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching ai agent")
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	err = models.RevokeAIAgent(ctx.OrgID, existing.ID, ctx.UserEmail)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed revoking ai agent")
	}
}

func toResponse(a models.AIAgent) openapi.AIAgentResponse {
	return openapi.AIAgentResponse{
		ID:            a.ID,
		OrgID:         a.OrgID,
		Name:          a.Name,
		MaskedKey:     a.MaskedKey,
		Status:        openapi.AIAgentStatusType(a.Status),
		Groups:        a.Groups,
		CreatedBy:     a.CreatedBy,
		DeactivatedBy: a.DeactivatedBy,
		CreatedAt:     a.CreatedAt,
		DeactivatedAt: a.DeactivatedAt,
		LastUsedAt:    a.LastUsedAt,
	}
}
