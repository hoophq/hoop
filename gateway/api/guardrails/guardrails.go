package apiguardrails

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// CreateGuardRailRules
//
//	@Summary		Create Guard Rail Rules
//	@Description	Create Guard Rail Rules
//	@Tags			Guard Rails
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.GuardRailRuleRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.GuardRailRuleResponse
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/guardrails [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	// Filter out empty connection IDs
	validConnectionIDs := filterEmptyIDs(req.ConnectionIDs)

	rule := &models.GuardRailRules{
		ID:          uuid.NewString(),
		OrgID:       ctx.GetOrgID(),
		Name:        req.Name,
		Description: req.Description,
		Input:       req.Input,
		Output:      req.Output,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	err := models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, true)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	case nil:
		if err := upsertGuardrailRuleAttributes(ctx, rule.Name, req.Attributes); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed upserting guard rail rule attributes")
			return
		}
		c.JSON(http.StatusCreated, &openapi.GuardRailRuleResponse{
			ID:            rule.ID,
			Name:          rule.Name,
			Description:   rule.Description,
			Input:         rule.Input,
			Output:        rule.Output,
			ConnectionIDs: rule.ConnectionIDs,
			Attributes:    req.Attributes,
			CreatedAt:     rule.CreatedAt,
			UpdatedAt:     rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed creating guard rail rule")
	}
}

// UpdateGuardRailRules
//
//	@Summary		Update Guard Rail Rules
//	@Description	Update Guard Rail Rules
//	@Tags			Guard Rails
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.GuardRailRuleRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.GuardRailRuleResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/guardrails/{id} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	// Filter out empty connection IDs
	validConnectionIDs := filterEmptyIDs(req.ConnectionIDs)

	rule := &models.GuardRailRules{
		OrgID:       ctx.GetOrgID(),
		ID:          c.Param("id"),
		Name:        req.Name,
		Description: req.Description,
		Input:       req.Input,
		Output:      req.Output,
		UpdatedAt:   time.Now().UTC(),
	}

	// Update guardrail and associate connections in a single transaction
	err := models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, false)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	case nil:
		if err := upsertGuardrailRuleAttributes(ctx, rule.Name, req.Attributes); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed upserting guard rail rule attributes")
			return
		}
		c.JSON(http.StatusOK, &openapi.GuardRailRuleResponse{
			ID:            rule.ID,
			Name:          rule.Name,
			Description:   rule.Description,
			Input:         rule.Input,
			Output:        rule.Output,
			ConnectionIDs: rule.ConnectionIDs,
			Attributes:    req.Attributes,
			CreatedAt:     rule.CreatedAt,
			UpdatedAt:     rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed updating guard rail rule")
	}
}

// ListGuardRailRules
//
//	@Summary		List Guard Rail Rules
//	@Description	List Guard Rail Rules
//	@Tags			Guard Rails
//	@Accept			json
//	@Produce		json
//	@Success		200	{array}		openapi.GuardRailRuleResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/guardrails [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ruleList, err := models.ListGuardRailRules(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing guard rail rules")
		return
	}

	rules := []openapi.GuardRailRuleResponse{}
	for _, rule := range ruleList {
		rules = append(rules, openapi.GuardRailRuleResponse{
			ID:            rule.ID,
			Name:          rule.Name,
			Description:   rule.Description,
			Input:         rule.Input,
			Output:        rule.Output,
			ConnectionIDs: rule.ConnectionIDs,
			Attributes:    rule.Attributes,
			CreatedAt:     rule.CreatedAt,
			UpdatedAt:     rule.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, rules)
}

// GetGuardRailRules
//
//	@Summary		Get Guard Rail Rules
//	@Description	Get Guard Rail Rules
//	@Tags			Guard Rails
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string	true	"The unique identifier of the resource"
//	@Success		200			{object}	openapi.GuardRailRuleResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/guardrails/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	rule, err := models.GetGuardRailRules(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, &openapi.GuardRailRuleResponse{
			ID:            rule.ID,
			Name:          rule.Name,
			Description:   rule.Description,
			Input:         rule.Input,
			Output:        rule.Output,
			ConnectionIDs: rule.ConnectionIDs, Attributes: rule.Attributes, CreatedAt: rule.CreatedAt,
			UpdatedAt: rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing guard rail rules")
	}
}

// DeleteRule
//
//	@Summary		Delete a Rule
//	@Description	Delete a Guard Rail Rule resource.
//	@Tags			Guard Rails
//	@Produce		json
//	@Param			id	path	string	true	"The unique identifier of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/guardrails/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ruleID := c.Param("id")

	err := models.DeleteGuardRailRules(ctx.GetOrgID(), ruleID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed removing guard rail rules")
	}
}

func parseRequestPayload(c *gin.Context) *openapi.GuardRailRuleRequest {
	req := openapi.GuardRailRuleRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return nil
	}
	return &req
}

// Helper to filter out empty connection IDs
func filterEmptyIDs(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			result = append(result, id)
		}
	}
	return result
}

func upsertGuardrailRuleAttributes(ctx *storagev2.Context, ruleName string, attributeNames []string) error {
	orgID := uuid.MustParse(ctx.GetOrgID())
	return models.UpsertGuardrailRuleAttributes(models.DB, orgID, ruleName, attributeNames)
}
