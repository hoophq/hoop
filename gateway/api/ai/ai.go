package apiai

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

func validateProviderRequest(p *openapi.AIProviderRequest) error {
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

// GetAIProvider
//
//	@Summary		Get AI Provider
//	@Description	Get the AI provider configured for the organization
//	@Tags			AI
//	@Produce		json
//	@Success		200		{object}	openapi.AIProviderResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai/providers [get]
func GetProvider(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	p, err := models.GetAIProvider(orgID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toProviderResponse(p))
	default:
		log.Errorf("failed fetching AI provider, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// UpsertAIProvider
//
//	@Summary		Upsert AI Provider
//	@Description	Create or update the AI provider for the organization (one per org)
//	@Tags			AI
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.AIProviderRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.AIProviderResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/ai/providers [post]
func UpsertProvider(c *gin.Context) {
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

	if err := validateProviderRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	p, err := models.UpsertAIProvider(orgID, req.Provider, req.ApiUrl, req.ApiKey, req.Model)
	if err != nil {
		log.Errorf("failed upserting AI provider, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toProviderResponse(p))
}

// DeleteAIProvider
//
//	@Summary		Delete AI Provider
//	@Description	Delete the AI provider configured for the organization
//	@Tags			AI
//	@Produce		json
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/ai/providers [delete]
func DeleteProvider(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	err = models.DeleteAIProvider(orgID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed deleting AI provider, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// ListAISessionAnalyzerRules
//
//	@Summary		List AI Session Analyzer Rules
//	@Description	List all AI session analyzer rules for the organization
//	@Tags			AI
//	@Produce		json
//	@Success		200	{array}		openapi.AISessionAnalyzerRuleResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/ai/session-analyzer/rules [get]
func ListSessionAnalyzerRules(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	rules, err := models.ListAISessionAnalyzerRules(orgID)
	if err != nil {
		log.Errorf("failed listing AI session analyzer rules, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	resp := make([]openapi.AISessionAnalyzerRule, 0, len(rules))
	for _, r := range rules {
		resp = append(resp, toSessionAnalyzerRuleResponse(r))
	}
	c.JSON(http.StatusOK, resp)
}

// GetAISessionAnalyzerRule
//
//	@Summary		Get AI Session Analyzer Rule
//	@Description	Get an AI session analyzer rule by name
//	@Tags			AI
//	@Produce		json
//	@Param			name		path		string	true	"The name of the resource"
//	@Success		200			{object}	openapi.AISessionAnalyzerRuleResponse
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
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toSessionAnalyzerRuleResponse(rule))
	default:
		log.Errorf("failed fetching AI session analyzer rule, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
//	@Success		201			{object}	openapi.AISessionAnalyzerRuleResponse
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
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
		c.JSON(http.StatusCreated, toSessionAnalyzerRuleResponse(rule))
	default:
		log.Errorf("failed creating AI session analyzer rule, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
//	@Success		200		{object}	openapi.AISessionAnalyzerRuleResponse
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
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toSessionAnalyzerRuleResponse(rule))
	default:
		log.Errorf("failed updating AI session analyzer rule, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed deleting AI session analyzer rule, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func toProviderResponse(p *models.AIProvider) openapi.AIProviderResponse {
	return openapi.AIProviderResponse{
		ID:        p.ID.String(),
		Provider:  *p.Provider,
		ApiUrl:    p.ApiUrl,
		Model:     p.Model,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
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
