package apiai

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

func validateProviderRequest(p openapi.AIProviderRequest) error {
	switch p.Provider {
	case "openai", "anthropic":
		return nil
	case "azure-openai", "custom":
		if p.ApiUrl == nil || *p.ApiUrl == "" {
			return fmt.Errorf("api_url is required for provider %s", p.Provider)
		}
		return nil
	default:
		return fmt.Errorf("unsupported provider: %s", p.Provider)
	}
}

// GetSessionAnalyzerProvider
//
//	@Summary		Get AI Session Analyzer Provider
//	@Description	Get the AI provider configured for the session analyzer feature in the organization
//	@Tags			AI
//	@Produce		json
//	@Success		200		{object}	openapi.AIProviderResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/providers [get]
func GetSessionAnalyzerProvider(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	p, err := models.GetAIProvider(orgID, models.AISessionAnalyzerFeature)
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toProviderResponse(*p))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI provider")
	}
}

// UpsertSessionAnalyzerProvider
//
//	@Summary		Upsert AI Session Analyzer Provider
//	@Description	Create or update the AI provider for the session analyzer feature in the organization (one per org)
//	@Tags			AI
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.AIProviderRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.AIProviderResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/providers [post]
func UpsertSessionAnalyzerProvider(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	var req openapi.AIProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := validateProviderRequest(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	p, err := models.UpsertAIProvider(orgID, models.AISessionAnalyzerFeature, req.Provider, req.ApiUrl, req.ApiKey, req.Model)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed upserting AI provider")
		return
	}

	analytics.New().Track(ctx.UserID, analytics.EventSessionAIAnalysisProviderUpdated, map[string]interface{}{
		"org-id":   p.OrgID,
		"provider": p.Provider,
		"model":    p.Model,
	})

	c.JSON(http.StatusOK, toProviderResponse(*p))
}

// DeleteSessionAnalyzerProvider
//
//	@Summary		Delete AI Session Analyzer Provider
//	@Description	Delete the AI provider configured for the session analyzer feature in the organization
//	@Tags			AI
//	@Produce		json
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/providers [delete]
func DeleteSessionAnalyzerProvider(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	err = models.DeleteAIProvider(orgID, models.AISessionAnalyzerFeature)
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting AI provider")
	}
}

// ListAISessionAnalyzerRules
//
//	@Summary		List AI Session Analyzer Rules
//	@Description	List all AI session analyzer rules for the organization, optionally filtered by connection names
//	@Tags			AI
//	@Produce		json
//	@Param			connection_names	query		array	false	"Filter by connection names (can be repeated)"
//	@Param			page				query		integer	false	"Page number (default 1)"
//	@Param			page_size			query		integer	false	"Page size (default 0 = all, max 100)"
//	@Success		200	{object}	openapi.PaginatedResponse[openapi.AISessionAnalyzerRule]
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules [get]
func ListSessionAnalyzerRules(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	connectionNames := c.QueryArray("connection_names")

	page := 1
	if p := c.Query("page"); p != "" {
		if parsed, err := fmt.Sscanf(p, "%d", &page); err != nil || parsed == 0 {
			page = 1
		}
	}

	pageSize := 0
	if ps := c.Query("page_size"); ps != "" {
		if parsed, err := fmt.Sscanf(ps, "%d", &pageSize); err != nil || parsed == 0 {
			pageSize = 0
		}
		if pageSize > 100 {
			pageSize = 100
		}
	}

	rules, total, err := models.ListAISessionAnalyzerRules(orgID, connectionNames, page, pageSize)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing AI session analyzer rules")
		return
	}

	resp := make([]openapi.AISessionAnalyzerRule, 0, len(rules))
	for _, r := range rules {
		resp = append(resp, toSessionAnalyzerRuleResponse(r))
	}

	paginatedResp := openapi.PaginatedResponse[openapi.AISessionAnalyzerRule]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  page,
			Size:  pageSize,
		},
		Data: resp,
	}
	c.JSON(http.StatusOK, paginatedResp)
}

// GetAISessionAnalyzerRule
//
//	@Summary		Get AI Session Analyzer Rule
//	@Description	Get an AI session analyzer rule by name
//	@Tags			AI
//	@Produce		json
//	@Param			name		path		string	true	"The name of the resource"
//	@Success		200			{object}	openapi.AISessionAnalyzerRule
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules/{name} [get]
func GetSessionAnalyzerRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	rule, err := models.GetAISessionAnalyzerRule(orgID, c.Param("name"))
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toSessionAnalyzerRuleResponse(rule))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI session analyzer rule")
	}
}

// CreateAISessionAnalyzerRule
//
//	@Summary		Create AI Session Analyzer Rule
//	@Description	Create a new AI session analyzer rule
//	@Tags			AI
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.AISessionAnalyzerRuleRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.AISessionAnalyzerRule
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules [post]
func CreateSessionAnalyzerRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	var req openapi.AISessionAnalyzerRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	foundRule, err := models.GetAIAnalyzerRulesByConnections(models.DB, orgID, req.ConnectionNames)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI analyzer rules by connections")
		return
	}

	if foundRule != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "another rule with the same connection names already exists"})
		return
	}

	rule := &models.AISessionAnalyzerRules{
		OrgID:           orgID,
		Name:            req.Name,
		Description:     req.Description,
		ConnectionNames: req.ConnectionNames,
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRiskAction:    models.RiskEvaluationAction(req.RiskEvaluation.LowRiskAction),
			MediumRiskAction: models.RiskEvaluationAction(req.RiskEvaluation.MediumRiskAction),
			HighRiskAction:   models.RiskEvaluationAction(req.RiskEvaluation.HighRiskAction),
		},
	}

	err = models.CreateAISessionAnalyzerRule(rule)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		analytics.New().Track(ctx.UserID, analytics.EventSessionAIAnalysisRuleCreated, map[string]interface{}{
			"org-id":             rule.OrgID,
			"low-risk-action":    rule.RiskEvaluation.LowRiskAction,
			"medium-risk-action": rule.RiskEvaluation.MediumRiskAction,
			"high-risk-action":   rule.RiskEvaluation.HighRiskAction,
		})
		c.JSON(http.StatusCreated, toSessionAnalyzerRuleResponse(rule))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating AI session analyzer rule")
	}
}

// UpdateAISessionAnalyzerRule
//
//	@Summary		Update AI Session Analyzer Rule
//	@Description	Update an existing AI session analyzer rule
//	@Tags			AI
//	@Accept			json
//	@Produce		json
//	@Param			name	path		string									true	"The name of the resource"
//	@Param			request	body		openapi.AISessionAnalyzerRuleRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.AISessionAnalyzerRule
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules/{name} [put]
func UpdateSessionAnalyzerRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	var req openapi.AISessionAnalyzerRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	foundRule, err := models.GetAIAnalyzerRulesByConnections(models.DB, orgID, req.ConnectionNames)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI analyzer rules by connections")
		return
	}

	if foundRule != nil && foundRule.Name != req.Name {
		c.JSON(http.StatusBadRequest, gin.H{"message": "another rule with the same connection names already exists"})
		return
	}

	rule := &models.AISessionAnalyzerRules{
		OrgID:           orgID,
		Name:            c.Param("name"),
		Description:     req.Description,
		ConnectionNames: req.ConnectionNames,
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRiskAction:    models.RiskEvaluationAction(req.RiskEvaluation.LowRiskAction),
			MediumRiskAction: models.RiskEvaluationAction(req.RiskEvaluation.MediumRiskAction),
			HighRiskAction:   models.RiskEvaluationAction(req.RiskEvaluation.HighRiskAction),
		},
	}

	err = models.UpdateAISessionAnalyzerRule(rule)
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		analytics.New().Track(ctx.UserID, analytics.EventSessionAIAnalysisRuleUpdated, map[string]interface{}{
			"org-id":             rule.OrgID,
			"low-risk-action":    rule.RiskEvaluation.LowRiskAction,
			"medium-risk-action": rule.RiskEvaluation.MediumRiskAction,
			"high-risk-action":   rule.RiskEvaluation.HighRiskAction,
		})

		c.JSON(http.StatusOK, toSessionAnalyzerRuleResponse(rule))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating AI session analyzer rule")
	}
}

// DeleteAISessionAnalyzerRule
//
//	@Summary		Delete AI Session Analyzer Rule
//	@Description	Delete an AI session analyzer rule
//	@Tags			AI
//	@Produce		json
//	@Param			name	path	string	true	"The name of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules/{name} [delete]
func DeleteSessionAnalyzerRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	err = models.DeleteAISessionAnalyzerRule(orgID, c.Param("name"))
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting AI session analyzer rule")
	}
}

func toProviderResponse(p models.AIProvider) openapi.AIProviderResponse {
	return openapi.AIProviderResponse{
		ID:        p.ID.String(),
		Provider:  p.Provider,
		ApiUrl:    p.ApiUrl,
		ApiKey:    p.ApiKey,
		Model:     p.Model,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

// GetConnectionAnalyzerRule
//
//	@Summary		Get AI Session Analyzer Rule by Connection
//	@Description	Get the AI session analyzer rule configured for a specific connection
//	@Tags			AI
//	@Produce		json
//	@Param			nameOrId	path		string	true	"The name or ID of the connection"
//	@Success		200			{object}	openapi.AISessionAnalyzerRule
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/connections/{nameOrId}/ai-session-analyzer-rule [get]
func GetConnectionAnalyzerRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	connectionNameOrID := c.Param("nameOrID")

	rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, connectionNameOrID)
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toSessionAnalyzerRuleResponse(rule))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI session analyzer rule by connection")
	}
}

func toSessionAnalyzerRuleResponse(r *models.AISessionAnalyzerRules) openapi.AISessionAnalyzerRule {
	return openapi.AISessionAnalyzerRule{
		ID:              r.ID.String(),
		Name:            r.Name,
		Description:     r.Description,
		ConnectionNames: r.ConnectionNames,
		RiskEvaluation: openapi.AISessionAnalyzerRiskEvaluation{
			LowRiskAction:    string(r.RiskEvaluation.LowRiskAction),
			MediumRiskAction: string(r.RiskEvaluation.MediumRiskAction),
			HighRiskAction:   string(r.RiskEvaluation.HighRiskAction),
		},
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}
