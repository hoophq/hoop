package apiopaconfigs

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// validateOPAConfigRequest keeps the validation shape-only on purpose: the
// gateway usually sits in a different network from the OPA the sidecar talks
// to, so a reachability probe would reject correct configurations.
func validateOPAConfigRequest(req *openapi.OPAConfigRequest) error {
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		return err
	}
	// Every name-or-id lookup resolves a parsable uuid as an id, so a
	// uuid-shaped name would create a row nothing can address by name.
	if _, err := uuid.Parse(req.Name); err == nil {
		return errors.New("name: it must not be a uuid, that spelling is reserved for addressing a configuration by its id")
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("url: it must be an absolute http or https decision endpoint, e.g. http://opa:8181/v1/data/hoop/inspect")
	}
	// url.Parse accepts any digit run as a port, and the sidecar only checks
	// that the URL is non-empty, so an unusable port would surface as every
	// decision failing at dial time rather than here.
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("url: the port must be between 1 and 65535")
		}
	}
	// The URL is stored in clear, echoed by the read endpoints an auditor can
	// call, and recorded verbatim in the audit log, which redacts by key name
	// and so would not redact a credential embedded here.
	if u.User != nil {
		return errors.New("url: it must not embed credentials, the audit log and the read endpoints would expose them")
	}
	if req.TimeoutSec < 0 || req.TimeoutSec > 300 {
		return errors.New("timeout_sec: it must be between 0 and 300, where 0 uses the sidecar default of 2 seconds")
	}
	if req.Gate {
		return errors.New("gate: not supported yet, the generated configuration carries no ai_analysis rules so a gated decision would gate nothing and the sidecar would refuse the whole configuration")
	}
	return nil
}

// Create OPA Configuration
//
//	@Summary		Create OPA Configuration
//	@Description	Register an OPA decision endpoint. A connection points at one by name or ID and it reaches the sidecar as the per-listener "opa" block.
//	@Tags			OPA
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.OPAConfigRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.OPAConfigResponse
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/opa-configs [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.OPAConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := validateOPAConfigRequest(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	obj := &models.OPAConfig{
		OrgID:      ctx.OrgID,
		Name:       req.Name,
		URL:        req.URL,
		TimeoutSec: req.TimeoutSec,
		FailOpen:   req.FailOpen,
		Gate:       req.Gate,
		CreatedBy:  ctx.UserEmail,
	}
	switch err := models.CreateOPAConfig(models.DB, obj); {
	case err == nil:
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "an opa configuration with this name already exists"})
		return
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating opa configuration")
		return
	}

	item, err := models.GetOPAConfigByNameOrID(models.DB, ctx.OrgID, obj.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching opa configuration")
		return
	}
	c.JSON(http.StatusCreated, toResponse(*item))
}

// List OPA Configurations
//
//	@Summary		List OPA Configurations
//	@Description	List all OPA decision endpoints for the organization
//	@Tags			OPA
//	@Produce		json
//	@Success		200	{array}		openapi.OPAConfigResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/opa-configs [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListOPAConfigs(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing opa configurations")
		return
	}
	result := []openapi.OPAConfigResponse{}
	for _, item := range items {
		result = append(result, toResponse(item))
	}
	c.JSON(http.StatusOK, result)
}

// Get OPA Configuration
//
//	@Summary		Get OPA Configuration
//	@Description	Get an OPA decision endpoint by name or ID
//	@Tags			OPA
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the opa configuration"
//	@Success		200			{object}	openapi.OPAConfigResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/opa-configs/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetOPAConfigByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "opa configuration not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching opa configuration")
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Update OPA Configuration
//
//	@Summary		Update OPA Configuration
//	@Description	Update an OPA decision endpoint. The connections pointed at it pick up the change on their next sidecar configuration fetch.
//	@Tags			OPA
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID			path		string						true	"Name or UUID of the opa configuration"
//	@Param			request				body		openapi.OPAConfigRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.OPAConfigResponse
//	@Failure		400,404,409,422,500	{object}	openapi.HTTPError
//	@Router			/opa-configs/{nameOrID} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.OPAConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := validateOPAConfigRequest(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	obj := &models.OPAConfig{
		Name:       req.Name,
		URL:        req.URL,
		TimeoutSec: req.TimeoutSec,
		FailOpen:   req.FailOpen,
		Gate:       req.Gate,
	}
	updatedID, err := models.UpdateOPAConfigByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"), obj)
	switch {
	case err == nil:
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "opa configuration not found"})
		return
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "an opa configuration with this name already exists"})
		return
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating opa configuration")
		return
	}

	// By id, not by the new name: the name is what this request just changed,
	// so reading it back is a race against the next rename.
	item, err := models.GetOPAConfigByNameOrID(models.DB, ctx.OrgID, updatedID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching opa configuration")
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Delete OPA Configuration
//
//	@Summary		Delete OPA Configuration
//	@Description	Delete an OPA decision endpoint. Refused while any connection points at it, because losing policy enforcement silently is worse than an error.
//	@Tags			OPA
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the opa configuration"
//	@Success		204
//	@Failure		404,409,500	{object}	openapi.HTTPError
//	@Router			/opa-configs/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetOPAConfigByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "opa configuration not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching opa configuration")
		return
	}
	if len(item.Connections) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"message": "opa configuration is assigned to connection(s): " + strings.Join(item.Connections, ", "),
		})
		return
	}
	// The foreign key is ON DELETE RESTRICT, so a race that slips past the
	// pre-check still fails in the database.
	if err := models.DeleteOPAConfigByID(models.DB, ctx.OrgID, item.ID); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "opa configuration not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting opa configuration")
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

func toResponse(o models.OPAConfig) openapi.OPAConfigResponse {
	connections := []string{}
	connections = append(connections, o.Connections...)
	return openapi.OPAConfigResponse{
		ID:          o.ID,
		OrgID:       o.OrgID,
		Name:        o.Name,
		URL:         o.URL,
		TimeoutSec:  o.TimeoutSec,
		FailOpen:    o.FailOpen,
		Gate:        o.Gate,
		Connections: connections,
		CreatedBy:   o.CreatedBy,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
}
