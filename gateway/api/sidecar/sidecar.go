package apisidecar

import (
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// reservedNames would shadow the static routes registered beside
// /sidecars/:nameOrID.
var reservedNames = []string{"handshake", "configuration"}

// Create Sidecar
//
//	@Summary		Create Sidecar
//	@Description	Register a sidecar. The token is returned only once in this response and cannot be recovered.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.SidecarRequest	true	"The request body resource"
//	@Success		201					{object}	openapi.SidecarCreateResponse
//	@Failure		400,409,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if slices.Contains(reservedNames, req.Name) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "name \"" + req.Name + "\" is reserved"})
		return
	}

	cfg, err := services.ParseSidecarConfiguration(req.Configuration)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	rawKey, err := services.GenerateSidecarKey()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar")
		return
	}
	sidecar := &models.Sidecar{
		OrgID:         ctx.OrgID,
		Name:          req.Name,
		KeyHash:       models.HashAPIKey(rawKey),
		Configuration: models.SidecarConfiguration(cfg),
		CreatedBy:     ctx.UserEmail,
	}

	switch err := models.CreateSidecar(models.DB, sidecar); {
	case err == nil:
		c.JSON(http.StatusCreated, openapi.SidecarCreateResponse{
			SidecarResponse: toResponse(*sidecar),
			Token:           rawKey,
		})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "a sidecar with this name already exists"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar")
	}
}

// List Sidecars
//
//	@Summary		List Sidecars
//	@Description	List all sidecars for the organization
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200	{array}		openapi.SidecarResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/sidecars [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListSidecars(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sidecars")
		return
	}
	result := []openapi.SidecarResponse{}
	for _, item := range items {
		result = append(result, toResponse(item))
	}
	c.JSON(http.StatusOK, result)
}

// Get Sidecar
//
//	@Summary		Get Sidecar
//	@Description	Get a sidecar by name or ID
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the sidecar"
//	@Success		200			{object}	openapi.SidecarResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar")
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Delete Sidecar
//
//	@Summary		Delete Sidecar
//	@Description	Delete a sidecar. The token stops working immediately.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the sidecar"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	deletedID, err := models.DeleteSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting sidecar")
		return
	}
	forgetRuntime(deletedID)
	c.Writer.WriteHeader(http.StatusNoContent)
}

// Update Sidecar
//
//	@Summary		Update Sidecar
//	@Description	Replace the configuration a sidecar serves. The sidecar picks it up on its next heartbeat.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID			path		string							true	"Name or UUID of the sidecar"
//	@Param			request				body		openapi.SidecarUpdateRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarResponse
//	@Failure		400,404,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	cfg, err := services.ParseSidecarConfiguration(req.Configuration)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	item, err := models.UpdateSidecarConfiguration(models.DB, ctx.OrgID,
		c.Param("nameOrID"), models.SidecarConfiguration(cfg))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating sidecar")
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Sidecar Handshake
//
//	@Summary		Sidecar Handshake
//	@Description	Authenticated with the hoop-sidecar-token header. Records the reported version and returns the configuration the sidecar must serve. Answers 412 while no configuration with listeners is assigned, recording nothing: a sidecar that cannot run must not show up as recently seen.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string							true	"The token returned when the sidecar was created"
//	@Param			request				body		openapi.SidecarHandshakeRequest	true	"The request body resource"
//	@Success		200				{object}	map[string]interface{}
//	@Failure		400,401,412,500	{object}	openapi.HTTPError
//	@Router			/sidecars/handshake [post]
func Handshake(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	var req openapi.SidecarHandshakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// An empty answer would only kill the caller: the sidecar refuses to
	// serve a config with no listeners and exits. The 412 lets it import
	// its local file instead, and skipping recordRuntime keeps a process
	// that cannot run from showing up as recently seen.
	if len(sidecar.Configuration.Listeners) == 0 {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "no configuration is assigned to this sidecar; " +
			"start the sidecar with its config file to import it, or author the configuration in the control plane"})
		return
	}
	recordRuntime(sidecar.ID, req.Version)
	c.JSON(http.StatusOK, sidecar.Configuration)
}

// Import Sidecar Configuration
//
//	@Summary		Import Sidecar Configuration
//	@Description	Authenticated with the hoop-sidecar-token header. Stores the config document a sidecar carried locally, once: the import is refused with 409 when the control plane already holds a configuration with listeners, so a centrally authored config is never overwritten by a restarting sidecar.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string	true	"The token returned when the sidecar was created"
//	@Param			request				body		object	true	"The configuration document, the same shape the handshake answers"
//	@Success		200					{object}	map[string]interface{}
//	@Failure		400,401,409,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/configuration [put]
func ImportConfiguration(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	cfg, err := services.ParseSidecarConfiguration(raw)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if len(cfg.Listeners) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "an imported configuration must declare at least one listener"})
		return
	}
	item, err := models.AdoptSidecarConfiguration(models.DB, sidecar.OrgID, sidecar.ID, models.SidecarConfiguration(cfg))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, item.Configuration)
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "the control plane already holds a configuration for this sidecar; edit it there"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed importing sidecar configuration")
	}
}

// Sidecar Configuration
//
//	@Summary		Sidecar Configuration
//	@Description	Authenticated with the hoop-sidecar-token header. Returns the configuration the sidecar must serve. Unlike the handshake it records nothing, so a poll never overwrites what the sidecar last reported about itself.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string	true	"The token returned when the sidecar was created"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		401,500	{object}	openapi.HTTPError
//	@Router			/sidecars/configuration [get]
func Configuration(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	c.JSON(http.StatusOK, sidecar.Configuration)
}

func toResponse(s models.Sidecar) openapi.SidecarResponse {
	resp := openapi.SidecarResponse{
		ID:            s.ID,
		OrgID:         s.OrgID,
		Name:          s.Name,
		CreatedBy:     s.CreatedBy,
		CreatedAt:     s.CreatedAt,
		Configuration: daemon.Config(s.Configuration),
	}
	if state := loadRuntime(s.ID); state != nil {
		resp.Version = state.Version
		lastSeen := state.LastSeen
		resp.LastSeenAt = &lastSeen
	}
	return resp
}
