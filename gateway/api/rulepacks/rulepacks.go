package apirulepacks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
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
		ID:               rp.ID.String(),
		OrgID:            rp.OrgID.String(),
		DisplayName:      rp.DisplayName,
		Description:      rp.Description,
		Version:          rp.Version,
		Tags:             tags,
		IsManaged:        rp.IsManaged,
		DataMaskingRules: []openapi.DataMaskingRule{},
		GuardRailRules:   []openapi.GuardRailRuleResponse{},
		ConnectionNames:  []string{},
		CreatedAt:        rp.CreatedAt,
		UpdatedAt:        rp.UpdatedAt,
	}
}

// buildRulepackResponse fetches each nested rule kind plus the list of connections
// this rulepack has been applied to. Note: three queries per rulepack — callers
// iterating over a list pay 3N round trips. Acceptable while typical orgs have few
// rulepacks; revisit if rulepack counts grow.
func buildRulepackResponse(rp *models.Rulepack) (openapi.Rulepack, error) {
	resp := toResponse(rp)
	rulepackID := rp.ID

	dmRules, err := models.ListDataMaskingRules(rp.OrgID.String(), models.DataMaskingListOption{
		RulepackID: &rulepackID,
	})
	if err != nil {
		return resp, err
	}
	resp.DataMaskingRules = make([]openapi.DataMaskingRule, 0, len(dmRules))
	for i := range dmRules {
		resp.DataMaskingRules = append(resp.DataMaskingRules, dataMaskingRuleToOpenAPI(&dmRules[i]))
	}

	grRules, err := models.ListGuardRailRules(rp.OrgID.String(), models.GuardRailListOption{
		RulepackID: &rulepackID,
	})
	if err != nil {
		return resp, err
	}
	resp.GuardRailRules = make([]openapi.GuardRailRuleResponse, 0, len(grRules))
	for _, gr := range grRules {
		resp.GuardRailRules = append(resp.GuardRailRules, guardRailRuleToOpenAPI(gr))
	}

	var connectionNames []string
	err = models.DB.Table("private.connections_attributes ca").
		Joins("JOIN private.attributes a ON a.org_id = ca.org_id AND a.name = ca.attribute_name").
		Where("a.org_id = ? AND a.rulepack_id = ?", rp.OrgID, rp.ID).
		Order("ca.connection_name ASC").
		Pluck("ca.connection_name", &connectionNames).Error
	if err != nil {
		return resp, err
	}
	if connectionNames == nil {
		connectionNames = []string{}
	}
	resp.ConnectionNames = connectionNames

	return resp, nil
}

func dataMaskingRuleToOpenAPI(r *models.DataMaskingRule) openapi.DataMaskingRule {
	supported := make([]openapi.SupportedEntityTypesEntry, len(r.SupportedEntityTypes))
	for i, e := range r.SupportedEntityTypes {
		supported[i] = openapi.SupportedEntityTypesEntry{Name: e.Name, EntityTypes: e.EntityTypes}
	}
	custom := make([]openapi.CustomEntityTypesEntry, len(r.CustomEntityTypes))
	for i, e := range r.CustomEntityTypes {
		custom[i] = openapi.CustomEntityTypesEntry{
			Name:     e.Name,
			Regex:    e.Regex,
			DenyList: e.DenyList,
			Score:    e.Score,
		}
	}
	attrs := []string(r.Attributes)
	if attrs == nil {
		attrs = []string{}
	}
	return openapi.DataMaskingRule{
		ID: r.ID,
		DataMaskingRuleRequest: openapi.DataMaskingRuleRequest{
			Name:                    r.Name,
			Description:             r.Description,
			Attributes:              attrs,
			SupportedEntityTypes:    supported,
			ScoreThreshold:          r.ScoreThreshold,
			CustomEntityTypesEntrys: custom,
			UpdatedAt:               r.UpdatedAt,
		},
	}
}

func guardRailRuleToOpenAPI(r *models.GuardRailRules) openapi.GuardRailRuleResponse {
	attrs := r.Attributes
	if attrs == nil {
		attrs = []string{}
	}
	return openapi.GuardRailRuleResponse{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Input:       r.Input,
		Output:      r.Output,
		Attributes:  attrs,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// RulepackIDFromNullString converts a nullable rulepack_id stored as sql.NullString
// into the *string form used by openapi response types.
func RulepackIDFromNullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
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
		Version:     nil,
		Tags:        pq.StringArray(req.Tags),
		IsManaged:   false,
	}

	rules := services.RulepackRulesInput{
		DataMaskingRules: req.DataMaskingRules,
		GuardRailRules:   req.GuardRailRules,
	}
	rp, _, err = services.CreateRulepackWithRules(context.Background(), rp, rules)
	switch {
	case errors.Is(err, services.ErrRulepackInvalidDisplayName):
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case err == nil:
		resp, buildErr := buildRulepackResponse(rp)
		if buildErr != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, buildErr, "failed building rulepack response: %v", buildErr)
			return
		}
		c.JSON(http.StatusCreated, resp)
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
	existing.Tags = pq.StringArray(req.Tags)

	rules := services.RulepackRulesInput{
		DataMaskingRules: req.DataMaskingRules,
		GuardRailRules:   req.GuardRailRules,
	}
	updated, _, err := services.UpdateRulepackWithRules(context.Background(), existing, rules)
	switch {
	case errors.Is(err, services.ErrRulepackInvalidDisplayName):
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case err == nil:
		resp, buildErr := buildRulepackResponse(updated)
		if buildErr != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, buildErr, "failed building rulepack response: %v", buildErr)
			return
		}
		c.JSON(http.StatusOK, resp)
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
		resp, buildErr := buildRulepackResponse(rp)
		if buildErr != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, buildErr, "failed building rulepack response: %v", buildErr)
			return
		}
		data = append(data, resp)
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
		resp, buildErr := buildRulepackResponse(rp)
		if buildErr != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, buildErr, "failed building rulepack response: %v", buildErr)
			return
		}
		c.JSON(http.StatusOK, resp)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching rulepack: %v", err)
	}
}

// ApplyRulepack
//
//	@Summary		Apply Rulepack to Connections
//	@Description	Replace the set of connections this rulepack is applied to. After the call, the rulepack is attached to exactly the supplied connections (additions and removals as needed). Non-rulepack attributes on each affected connection are preserved. Pass an empty array to remove the rulepack from all connections. Returns 400 with a list of missing names if any connection in the request does not exist.
//	@Tags			Rulepacks
//	@Accept			json
//	@Produce		json
//	@Param			id				path	string							true	"Rulepack ID"
//	@Param			request			body	openapi.RulepackApplyRequest	true	"Connections to apply the rulepack to"
//	@Success		204
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/rulepacks/{id}/apply [post]
func Apply(c *gin.Context) {
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

	var req openapi.RulepackApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	err = services.ApplyRulepackToConnections(context.Background(), orgID, id, req.ConnectionNames)
	var notFound *services.ConnectionsNotFoundError
	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusBadRequest, gin.H{
			"message":       notFound.Error(),
			"missing_names": notFound.Names,
		})
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "rulepack not found"})
	case errors.Is(err, services.ErrRulepackHasNoAttribute):
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "%v", err)
	case err == nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed applying rulepack: %v", err)
	}
}
