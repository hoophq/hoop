package apirulepacks

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/lib/pq"
)

const flagName = "experimental.rulepacks"

func flagDisabled(c *gin.Context, ctx *storagev2.Context) bool {
	if featureflag.IsEnabled(ctx.GetOrgID(), flagName) {
		return false
	}
	c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	return true
}

func toResponse(rp *models.Rulepack) openapi.Rulepack {
	tags := []string(rp.Tags)
	if tags == nil {
		tags = []string{}
	}
	return openapi.Rulepack{
		ID:          rp.ID.String(),
		OrgID:       rp.OrgID.String(),
		DisplayName: rp.DisplayName,
		Description: rp.Description,
		Version:     rp.Version,
		Tags:        tags,
		IsManaged:   rp.IsManaged,
		IsPaid:      rp.IsPaid,
		CreatedAt:   rp.CreatedAt,
		UpdatedAt:   rp.UpdatedAt,
	}
}

// CreateRulepack
//
//	@Summary		Create Rulepack
//	@Description	Create a new rulepack for the organization. Always created with is_managed=false.
//	@Tags			Rulepacks
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.RulepackRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.Rulepack
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/rulepacks [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if flagDisabled(c, ctx) {
		return
	}

	var req openapi.RulepackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	rp := &models.Rulepack{
		OrgID:       orgID,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Version:     req.Version,
		Tags:        pq.StringArray(req.Tags),
		IsManaged:   false,
		IsPaid:      req.IsPaid,
	}

	switch err := models.CreateRulepack(models.DB, rp); err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusCreated, toResponse(rp))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating rulepack: %v", err)
	}
}

// UpdateRulepack
//
//	@Summary		Update Rulepack
//	@Description	Update an existing rulepack. Returns 403 if the rulepack is Hoop-managed.
//	@Tags			Rulepacks
//	@Accept			json
//	@Produce		json
//	@Param			id				path		string					true	"Rulepack ID"
//	@Param			request			body		openapi.RulepackRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.Rulepack
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/rulepacks/{id} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if flagDisabled(c, ctx) {
		return
	}

	var req openapi.RulepackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid rulepack id"})
		return
	}

	existing, err := models.GetRulepack(models.DB, orgID, id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching rulepack: %v", err)
		return
	}

	if existing.IsManaged {
		c.JSON(http.StatusForbidden, gin.H{"message": "managed rulepacks cannot be modified"})
		return
	}

	existing.DisplayName = req.DisplayName
	existing.Description = req.Description
	existing.Version = req.Version
	existing.Tags = pq.StringArray(req.Tags)
	existing.IsPaid = req.IsPaid

	switch err := models.UpdateRulepack(models.DB, existing); err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, toResponse(existing))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating rulepack: %v", err)
	}
}

// DeleteRulepack
//
//	@Summary		Delete Rulepack
//	@Description	Delete a rulepack. Returns 403 if Hoop-managed. Cascades delete to its attributes and their feature junctions.
//	@Tags			Rulepacks
//	@Produce		json
//	@Param			id				path	string	true	"Rulepack ID"
//	@Success		204
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/rulepacks/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if flagDisabled(c, ctx) {
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid rulepack id"})
		return
	}

	existing, err := models.GetRulepack(models.DB, orgID, id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching rulepack: %v", err)
		return
	}

	if existing.IsManaged {
		c.JSON(http.StatusForbidden, gin.H{"message": "managed rulepacks cannot be deleted"})
		return
	}

	switch err := models.DeleteRulepack(models.DB, orgID, id); err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting rulepack: %v", err)
	}
}

// ListRulepacks
//
//	@Summary		List Rulepacks
//	@Description	List rulepacks for the organization with optional pagination and search.
//	@Tags			Rulepacks
//	@Produce		json
//	@Param			search		query		string	false	"Search by display_name"
//	@Param			page		query		int		false	"Page number (1-based)"
//	@Param			page_size	query		int		false	"Items per page (1-100)"
//	@Success		200			{object}	openapi.PaginatedResponse[openapi.Rulepack]
//	@Failure		422,500		{object}	openapi.HTTPError
//	@Router			/rulepacks [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if flagDisabled(c, ctx) {
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	q := c.Request.URL.Query()
	page, pageSize, parseErr := apivalidation.ParsePaginationParams(q.Get("page"), q.Get("page_size"))
	if parseErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": parseErr.Error()})
		return
	}

	rps, total, err := models.ListRulepacks(models.DB, orgID, models.RulepackFilterOption{
		Search:   q.Get("search"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing rulepacks: %v", err)
		return
	}

	data := make([]openapi.Rulepack, 0, len(rps))
	for _, rp := range rps {
		data = append(data, toResponse(rp))
	}

	c.JSON(http.StatusOK, openapi.PaginatedResponse[openapi.Rulepack]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  page,
			Size:  pageSize,
		},
		Data: data,
	})
}

// GetRulepack
//
//	@Summary		Get Rulepack
//	@Description	Get a rulepack by its UUID.
//	@Tags			Rulepacks
//	@Produce		json
//	@Param			id				path		string	true	"Rulepack ID"
//	@Success		200				{object}	openapi.Rulepack
//	@Failure		400,404,500		{object}	openapi.HTTPError
//	@Router			/rulepacks/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if flagDisabled(c, ctx) {
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid rulepack id"})
		return
	}

	rp, err := models.GetRulepack(models.DB, orgID, id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toResponse(rp))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching rulepack: %v", err)
	}
}
