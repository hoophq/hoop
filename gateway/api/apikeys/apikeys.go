package apikeys

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// List API Keys
//
//	@Summary		List API Keys
//	@Description	List all API keys for the organization
//	@Tags			API Keys
//	@Produce		json
//	@Success		200	{array}		openapi.APIKeyResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/api-keys [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAPIKeys(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing api keys")
		return
	}
	var resp []openapi.APIKeyResponse
	for _, item := range items {
		resp = append(resp, toResponse(item))
	}
	c.JSON(http.StatusOK, resp)
}

// Get API Key
//
//	@Summary		Get API Key
//	@Description	Get an API key by name or ID
//	@Tags			API Keys
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the API key"
//	@Success		200			{object}	openapi.APIKeyResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/api-keys/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetAPIKeyByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching api key")
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Create API Key
//
//	@Summary		Create API Key
//	@Description	Generate a new API key. The raw key is returned only once in the response and cannot be retrieved after creation.
//	@Tags			API Keys
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.APIKeyCreateRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.APIKeyCreateResponse
//	@Failure		400,409,500		{object}	openapi.HTTPError
//	@Router			/api-keys [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.APIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	rawKey := models.GenerateAPIKey()
	apiKey := &models.APIKey{
		OrgID:         ctx.OrgID,
		Name:          req.Name,
		KeyHash:       models.HashAPIKey(rawKey),
		MaskedKey:     models.MaskAPIKey(rawKey),
		Status:        "active",
		Groups:        req.Groups,
		ConnectionIDs: req.ConnectionIDs,
		CreatedBy:     ctx.UserEmail,
	}

	err := models.CreateAPIKey(apiKey)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "an api key with this name already exists"})
	case nil:
		c.JSON(http.StatusCreated, openapi.APIKeyCreateResponse{
			APIKeyResponse: toResponse(*apiKey),
			Key:            rawKey,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating api key")
	}
}

// Update API Key
//
//	@Summary		Update API Key
//	@Description	Update an API key's name and/or groups. Only active keys can be updated.
//	@Tags			API Keys
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID		path		string						true	"Name or UUID of the API key"
//	@Param			request			body		openapi.APIKeyUpdateRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.APIKeyResponse
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/api-keys/{nameOrID} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.APIKeyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existing, err := models.GetAPIKeyByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching api key")
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}
	if existing.Status != "active" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot update a revoked api key"})
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
	connectionIDs := req.ConnectionIDs
	if connectionIDs == nil {
		connectionIDs = existing.ConnectionIDs
	}

	apiKey := &models.APIKey{
		ID:            existing.ID,
		OrgID:         ctx.OrgID,
		Name:          name,
		Groups:        groups,
		ConnectionIDs: connectionIDs,
	}

	err = models.UpdateAPIKey(apiKey)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "an api key with this name already exists"})
	case nil:
		updated, err := models.GetAPIKeyByNameOrID(ctx.OrgID, existing.ID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching updated api key")
			return
		}
		if updated == nil {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		c.JSON(http.StatusOK, toResponse(*updated))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating api key")
	}
}

// Revoke API Key
//
//	@Summary		Revoke API Key
//	@Description	Revoke an API key (soft delete). The key status is set to revoked and cannot be reactivated.
//	@Tags			API Keys
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the API key"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/api-keys/{nameOrID} [delete]
func Revoke(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	existing, err := models.GetAPIKeyByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching api key")
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	err = models.RevokeAPIKey(ctx.OrgID, existing.ID, ctx.UserEmail)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed revoking api key")
	}
}

func toResponse(ak models.APIKey) openapi.APIKeyResponse {
	return openapi.APIKeyResponse{
		ID:            ak.ID,
		OrgID:         ak.OrgID,
		Name:          ak.Name,
		MaskedKey:     ak.MaskedKey,
		Status:        openapi.APIKeyStatusType(ak.Status),
		Groups:        ak.Groups,
		ConnectionIDs: ak.ConnectionIDs,
		CreatedBy:     ak.CreatedBy,
		DeactivatedBy: ak.DeactivatedBy,
		CreatedAt:     ak.CreatedAt,
		DeactivatedAt: ak.DeactivatedAt,
		LastUsedAt:    ak.LastUsedAt,
	}
}
