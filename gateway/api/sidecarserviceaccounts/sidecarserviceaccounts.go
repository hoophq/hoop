// Package sidecarserviceaccounts implements the admin API for the mappings
// that let a sidecar authenticate with a platform service account token, and
// for the names of deleted sidecars those tokens must not create again.
package sidecarserviceaccounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const conflictMessage = "a sidecar service account with this name, or with this issuer, claim and subject_pattern, already exists"

// List Sidecar Service Accounts
//
//	@Summary		List Sidecar Service Accounts
//	@Description	List the service accounts whose tokens may authenticate a sidecar in the organization.
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200		{array}		openapi.SidecarServiceAccount
//	@Failure		403,500	{object}	openapi.HTTPError
//	@Router			/sidecar-service-accounts [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListSidecarServiceAccounts(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sidecar service accounts")
		return
	}
	out := make([]openapi.SidecarServiceAccount, 0, len(items))
	for _, sa := range items {
		out = append(out, toOpenAPI(sa))
	}
	c.JSON(http.StatusOK, out)
}

// Get Sidecar Service Account
//
//	@Summary		Get Sidecar Service Account
//	@Description	Get one sidecar service account by its ID.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			id			path		string	true	"Sidecar service account ID"
//	@Success		200			{object}	openapi.SidecarServiceAccount
//	@Failure		403,404,500	{object}	openapi.HTTPError
//	@Router			/sidecar-service-accounts/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sa, err := models.GetSidecarServiceAccount(models.DB, ctx.OrgID, c.Param("id"))
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar service account not found"})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the sidecar service account")
	default:
		c.JSON(http.StatusOK, toOpenAPI(*sa))
	}
}

// Create Sidecar Service Account
//
//	@Summary		Create Sidecar Service Account
//	@Description	Allow the tokens of a Kubernetes or Google service account to authenticate a sidecar with the hoop-sidecar-identity header. A matching token reaches the sidecar name_template renders, and creates it on its first handshake when no sidecar has that name. An issuer and audience pair belongs to one organization: 409 when another organization uses it.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.SidecarServiceAccount	true	"The request body resource"
//	@Success		201					{object}	openapi.SidecarServiceAccount
//	@Failure		400,403,409,422,500	{object}	openapi.HTTPError
//	@Router			/sidecar-service-accounts [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarServiceAccount
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	sa := toModel(req, ctx.OrgID)
	sa.CreatedBy = ctx.UserEmail
	if err := services.ValidateSidecarServiceAccount(sa); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	switch err := models.CreateSidecarServiceAccount(models.DB, sa); {
	case errors.Is(err, models.ErrSidecarServiceAccountPairTaken):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": conflictMessage})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating the sidecar service account")
	default:
		c.JSON(http.StatusCreated, toOpenAPI(*sa))
	}
}

// Update Sidecar Service Account
//
//	@Summary		Update Sidecar Service Account
//	@Description	Replace a sidecar service account. The sidecars it reached keep their names; a token it no longer matches stops authenticating. 409 when another organization uses the new issuer and audience.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			id					path		string							true	"Sidecar service account ID"
//	@Param			request				body		openapi.SidecarServiceAccount	true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarServiceAccount
//	@Failure		400,403,404,409,422,500	{object}	openapi.HTTPError
//	@Router			/sidecar-service-accounts/{id} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarServiceAccount
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	sa := toModel(req, ctx.OrgID)
	sa.ID = c.Param("id")
	if err := services.ValidateSidecarServiceAccount(sa); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	updated, err := models.UpdateSidecarServiceAccount(models.DB, sa)
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar service account not found"})
	case errors.Is(err, models.ErrSidecarServiceAccountPairTaken):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": conflictMessage})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating the sidecar service account")
	default:
		c.JSON(http.StatusOK, toOpenAPI(*updated))
	}
}

// Delete Sidecar Service Account
//
//	@Summary		Delete Sidecar Service Account
//	@Description	Remove a sidecar service account. The sidecars it reached stay; their tokens stop authenticating unless another sidecar service account allows them.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			id	path	string	true	"Sidecar service account ID"
//	@Success		204
//	@Failure		403,404,500	{object}	openapi.HTTPError
//	@Router			/sidecar-service-accounts/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	switch err := models.DeleteSidecarServiceAccount(models.DB, ctx.OrgID, c.Param("id")); {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar service account not found"})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting the sidecar service account")
	default:
		c.Status(http.StatusNoContent)
	}
}

// List Sidecar Deleted Names
//
//	@Summary		List Sidecar Deleted Names
//	@Description	List the names of deleted sidecars a service account identity had reached. A token that renders one of them is refused and creates nothing.
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200		{array}		openapi.SidecarDeletedName
//	@Failure		403,500	{object}	openapi.HTTPError
//	@Router			/sidecar-deleted-names [get]
func ListDeletedNames(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListSidecarDeletedNames(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing deleted sidecar names")
		return
	}
	out := make([]openapi.SidecarDeletedName, 0, len(items))
	for _, d := range items {
		out = append(out, openapi.SidecarDeletedName{Name: d.Name, DeletedBy: d.DeletedBy, DeletedAt: d.DeletedAt})
	}
	c.JSON(http.StatusOK, out)
}

// Clear Sidecar Deleted Name
//
//	@Summary		Clear Sidecar Deleted Name
//	@Description	Allow a service account identity to create a sidecar with this name again, on its next handshake.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			name	path	string	true	"The deleted sidecar name"
//	@Success		204
//	@Failure		403,404,500	{object}	openapi.HTTPError
//	@Router			/sidecar-deleted-names/{name} [delete]
func ClearDeletedName(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	switch err := models.DeleteSidecarDeletedName(models.DB, ctx.OrgID, c.Param("name")); {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "deleted sidecar name not found"})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed clearing the deleted sidecar name")
	default:
		c.Status(http.StatusNoContent)
	}
}

func toModel(req openapi.SidecarServiceAccount, orgID string) *models.SidecarServiceAccount {
	// "jwks": null names no key set, the same as leaving it out.
	jwks := json.RawMessage(bytes.TrimSpace(req.JWKS))
	if bytes.Equal(jwks, []byte("null")) || len(jwks) == 0 {
		jwks = nil
	}
	return &models.SidecarServiceAccount{
		OrgID:           orgID,
		Name:            req.Name,
		Issuer:          req.Issuer,
		Audience:        req.Audience,
		Claim:           req.Claim,
		SubjectPattern:  req.SubjectPattern,
		NameTemplate:    req.NameTemplate,
		JWKS:            jwks,
		AllowAnySubject: req.AllowAnySubject,
	}
}

func toOpenAPI(sa models.SidecarServiceAccount) openapi.SidecarServiceAccount {
	return openapi.SidecarServiceAccount{
		ID:              sa.ID,
		OrgID:           sa.OrgID,
		Name:            sa.Name,
		Issuer:          sa.Issuer,
		Audience:        sa.Audience,
		Claim:           sa.Claim,
		SubjectPattern:  sa.SubjectPattern,
		NameTemplate:    sa.NameTemplate,
		JWKS:            sa.JWKS,
		AllowAnySubject: sa.AllowAnySubject,
		CreatedBy:       sa.CreatedBy,
		CreatedAt:       sa.CreatedAt,
		UpdatedAt:       sa.UpdatedAt,
	}
}
