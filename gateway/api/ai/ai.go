package apiai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/aianalyzer"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/api/sidecarbind"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
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

func validateRiskTier(orgID uuid.UUID, level string, tier openapi.AISessionAnalyzerRiskTier) error {
	action := models.RiskEvaluationAction(tier.Action)
	if !action.IsValid() {
		return fmt.Errorf("%s_risk: invalid action %q", level, tier.Action)
	}
	if action == models.RequireAccessRequest {
		if tier.AccessRequestRuleName == nil || *tier.AccessRequestRuleName == "" {
			return fmt.Errorf("%s_risk: access_request_rule_name is required when action is require_access_request", level)
		}
		if _, err := models.GetAccessRequestRuleByName(models.DB, *tier.AccessRequestRuleName, orgID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%s_risk: access request rule %q not found", level, *tier.AccessRequestRuleName)
			}
			return fmt.Errorf("%s_risk: failed validating access request rule: %w", level, err)
		}
		return nil
	}
	if tier.AccessRequestRuleName != nil && *tier.AccessRequestRuleName != "" {
		return fmt.Errorf("%s_risk: access_request_rule_name is only allowed when action is require_access_request", level)
	}
	return nil
}

func resolveTier(level string, structured *openapi.AISessionAnalyzerRiskTier, legacy string) (openapi.AISessionAnalyzerRiskTier, error) {
	if structured != nil && structured.Action != "" {
		return *structured, nil
	}
	if legacy == "" {
		return openapi.AISessionAnalyzerRiskTier{}, fmt.Errorf("%s_risk: action is required (set either %s_risk.action or the legacy %s_risk_action field)", level, level, level)
	}
	return openapi.AISessionAnalyzerRiskTier{Action: legacy}, nil
}

func validateAnalyzerRuleRequest(orgID uuid.UUID, req openapi.AISessionAnalyzerRuleRequest) (low, medium, high openapi.AISessionAnalyzerRiskTier, err error) {
	low, err = resolveTier("low", req.RiskEvaluation.LowRisk, req.RiskEvaluation.LowRiskAction)
	if err != nil {
		return
	}
	medium, err = resolveTier("medium", req.RiskEvaluation.MediumRisk, req.RiskEvaluation.MediumRiskAction)
	if err != nil {
		return
	}
	high, err = resolveTier("high", req.RiskEvaluation.HighRisk, req.RiskEvaluation.HighRiskAction)
	if err != nil {
		return
	}
	if err = validateRiskTier(orgID, "low", low); err != nil {
		return
	}
	if err = validateRiskTier(orgID, "medium", medium); err != nil {
		return
	}
	if err = validateRiskTier(orgID, "high", high); err != nil {
		return
	}
	return
}

func toModelRiskTier(tier openapi.AISessionAnalyzerRiskTier) *models.AISessionAnalyzerRiskTier {
	return &models.AISessionAnalyzerRiskTier{
		Action:                models.RiskEvaluationAction(tier.Action),
		AccessRequestRuleName: tier.AccessRequestRuleName,
	}
}

func toOpenapiRiskTier(tier *models.AISessionAnalyzerRiskTier) *openapi.AISessionAnalyzerRiskTier {
	if tier == nil {
		return nil
	}
	return &openapi.AISessionAnalyzerRiskTier{
		Action:                string(tier.Action),
		AccessRequestRuleName: tier.AccessRequestRuleName,
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI provider: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed upserting AI provider: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting AI provider: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing AI session analyzer rules: %v", err)
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
		out := toSessionAnalyzerRuleResponse(rule)
		// Read back on the single-rule route, which is what the edit form
		// loads. Without it the form opens with the picker empty and the next
		// save unbinds the rule from every sidecar it reached, or drops its
		// reviewers. So a failed read answers 500, never a rule that claims
		// to have none.
		targets, err := sidecarbind.LoadStrict(ctx.GetOrgID(), services.SidecarRuleAnalyzer, rule.Name)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the sidecar targets of the rule")
			return
		}
		reviewers, err := storedHoldReviewers(orgID, rule.Name)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the reviewer groups of the rule")
			return
		}
		out.SidecarTargets = targets
		out.ReviewersGroups = reviewers
		c.JSON(http.StatusOK, out)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI session analyzer rule: %v", err)
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

	lowTier, mediumTier, highTier, err := validateAnalyzerRuleRequest(orgID, req)
	if err != nil {
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
		CustomPrompt:    req.CustomPrompt,
		Agentic:         req.Agentic,
		SidecarSpec:     req.SidecarSpec,
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRisk:    toModelRiskTier(lowTier),
			MediumRisk: toModelRiskTier(mediumTier),
			HighRisk:   toModelRiskTier(highTier),
		},
	}

	if sidecarbind.Refuse(c, ctx.GetOrgID(), sidecarbind.Request{
		Kind: services.SidecarRuleAnalyzer, Name: rule.Name, StoredName: "",
		Spec: req.SidecarSpec, Targets: req.SidecarTargets,
	}) {
		return
	}

	// One transaction: the rule row and its sidecar bindings. A binding that
	// fails after the rule row commits leaves the OLD bindings serving the NEW
	// spec, which is the pairing sidecarbind.Refuse rejects.
	var bindErr error
	var holdErr error
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.CreateAISessionAnalyzerRuleTx(tx, rule); err != nil {
			return err
		}
		// The rule that says who may release a statement this one holds. In
		// the same transaction, because a rule that holds and cannot release
		// denies every matching statement with no review anyone can approve.
		if holdErr = services.SyncAnalyzerApprovalRule(tx, orgID, rule.Name, rule.SidecarSpec, holdReviewers(req)); holdErr != nil {
			return holdErr
		}
		bindErr = sidecarbind.PersistTx(tx, ctx.GetOrgID(), services.SidecarRuleAnalyzer, rule.Name, req.SidecarTargets)
		return bindErr
	})
	if holdErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": holdErr.Error()})
		return
	}
	if bindErr != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, bindErr, "failed binding the rule to its sidecars: %v", bindErr)
		return
	}
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		analytics.New().Track(ctx.UserID, analytics.EventSessionAIAnalysisRuleCreated, map[string]interface{}{
			"org-id":             rule.OrgID,
			"low-risk-action":    rule.RiskEvaluation.Tier(models.RiskLevelKeyLow).Action,
			"medium-risk-action": rule.RiskEvaluation.Tier(models.RiskLevelKeyMedium).Action,
			"high-risk-action":   rule.RiskEvaluation.Tier(models.RiskLevelKeyHigh).Action,
		})
		out := toSessionAnalyzerRuleResponse(rule)
		out.SidecarTargets = sidecarbind.Load(ctx.GetOrgID(), services.SidecarRuleAnalyzer, rule.Name)
		out.ReviewersGroups = holdReviewersOf(orgID, rule.Name)
		c.JSON(http.StatusCreated, out)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating AI session analyzer rule: %v", err)
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

	// Read once and keep it: the managed-by refusal below and the sidecar
	// block this write preserves both come off the stored row.
	existing, gerr := models.GetAISessionAnalyzerRule(orgID, c.Param("name"))
	if gerr == nil && existing.ManagedBy != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is managed by Hoop and cannot be modified directly"})
		return
	}
	var storedSpec json.RawMessage
	if gerr == nil && existing != nil {
		storedSpec = existing.SidecarSpec
	}

	lowTier, mediumTier, highTier, err := validateAnalyzerRuleRequest(orgID, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	foundRule, err := models.GetAIAnalyzerRulesByConnections(models.DB, orgID, req.ConnectionNames)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching AI analyzer rules by connections: %v", err)
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
		CustomPrompt:    req.CustomPrompt,
		Agentic:         req.Agentic,
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRisk:    toModelRiskTier(lowTier),
			MediumRisk: toModelRiskTier(mediumTier),
			HighRisk:   toModelRiskTier(highTier),
		},
	}

	// The sidecar block this write leaves on the rule, which is what the gate
	// checks and what the row stores. A request that says nothing about it
	// keeps the stored one rather than clearing it, so an edit to the prompt
	// or the connections does not silently disarm a bound rule.
	bind := sidecarbind.Request{
		Kind: services.SidecarRuleAnalyzer, Name: rule.Name, StoredName: rule.Name,
		Spec: req.SidecarSpec, StoredSpec: storedSpec, Targets: req.SidecarTargets,
	}
	if sidecarbind.Refuse(c, ctx.GetOrgID(), bind) {
		return
	}
	rule.SidecarSpec = bind.EffectiveSpec()

	var bindErr error
	var holdErr error
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.UpdateAISessionAnalyzerRuleTx(tx, rule); err != nil {
			return err
		}
		// Present while the rule holds, gone once it stops: switching the hold
		// off has to take the approval rule with it, or the fleet keeps a
		// reviewer list for a statement nothing holds any more.
		if holdErr = services.SyncAnalyzerApprovalRule(tx, orgID, rule.Name, rule.SidecarSpec, holdReviewers(req)); holdErr != nil {
			return holdErr
		}
		bindErr = sidecarbind.PersistTx(tx, ctx.GetOrgID(), services.SidecarRuleAnalyzer, rule.Name, req.SidecarTargets)
		return bindErr
	})
	if holdErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": holdErr.Error()})
		return
	}
	if bindErr != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, bindErr, "failed binding the rule to its sidecars: %v", bindErr)
		return
	}
	switch err {
	case gorm.ErrRecordNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		analytics.New().Track(ctx.UserID, analytics.EventSessionAIAnalysisRuleUpdated, map[string]interface{}{
			"org-id":             rule.OrgID,
			"low-risk-action":    rule.RiskEvaluation.Tier(models.RiskLevelKeyLow).Action,
			"medium-risk-action": rule.RiskEvaluation.Tier(models.RiskLevelKeyMedium).Action,
			"high-risk-action":   rule.RiskEvaluation.Tier(models.RiskLevelKeyHigh).Action,
		})

		out := toSessionAnalyzerRuleResponse(rule)
		out.SidecarTargets = sidecarbind.Load(ctx.GetOrgID(), services.SidecarRuleAnalyzer, rule.Name)
		out.ReviewersGroups = holdReviewersOf(orgID, rule.Name)
		c.JSON(http.StatusOK, out)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating AI session analyzer rule: %v", err)
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

	if existing, gerr := models.GetAISessionAnalyzerRule(orgID, c.Param("name")); gerr == nil && existing.ManagedBy != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is managed by Hoop and cannot be deleted directly"})
		return
	}

	// The approval rule goes with it. Left behind it would be a reviewer list
	// in a control plane with no page to remove it from, and the next analyzer
	// rule of the same name would refuse to save over it.
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.DeleteAISessionAnalyzerRuleTx(tx, orgID, c.Param("name")); err != nil {
			return err
		}
		return services.DeleteAnalyzerApprovalRule(tx, orgID, c.Param("name"))
	})
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
	lowTier := r.RiskEvaluation.Tier(models.RiskLevelKeyLow)
	mediumTier := r.RiskEvaluation.Tier(models.RiskLevelKeyMedium)
	highTier := r.RiskEvaluation.Tier(models.RiskLevelKeyHigh)
	return openapi.AISessionAnalyzerRule{
		ID:              r.ID.String(),
		Name:            r.Name,
		Description:     r.Description,
		ConnectionNames: r.ConnectionNames,
		ManagedBy:       r.ManagedBy,
		CustomPrompt:    r.CustomPrompt,
		Agentic:         r.Agentic,
		SidecarSpec:     r.SidecarSpec,
		RiskEvaluation: openapi.AISessionAnalyzerRiskEvaluation{
			LowRiskAction:    string(lowTier.Action),
			MediumRiskAction: string(mediumTier.Action),
			HighRiskAction:   string(highTier.Action),
			LowRisk:          toOpenapiRiskTier(&lowTier),
			MediumRisk:       toOpenapiRiskTier(&mediumTier),
			HighRisk:         toOpenapiRiskTier(&highTier),
		},
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// GetSessionAnalyzerSystemPrompt
//
//	@Summary		Get AI Session Analyzer System Prompt
//	@Description	Returns the read-only system prompt that the gateway prepends before any custom_prompt configured on a rule
//	@Tags			AI
//	@Produce		json
//	@Success		200	{object}	openapi.AISessionAnalyzerSystemPrompt
//	@Router			/ai/session-analyzer/system-prompt [get]
func GetSessionAnalyzerSystemPrompt(c *gin.Context) {
	c.JSON(http.StatusOK, openapi.AISessionAnalyzerSystemPrompt{
		Prompt: aianalyzer.SessionAnalyzerSystemPrompt,
	})
}

// holdReviewers is the reviewer groups the request names for the rule's hold.
// Only a control plane holds statements; a gateway always passes nil.
func holdReviewers(req openapi.AISessionAnalyzerRuleRequest) *[]string {
	if !appconfig.Get().IsControlPlane() {
		return nil
	}
	return req.ReviewersGroups
}

// holdReviewersOf reads back who may release what the rule holds, for the
// edit form. It is nil when the rule does not hold, and outside a control
// plane.
func holdReviewersOf(orgID uuid.UUID, ruleName string) []string {
	groups, err := storedHoldReviewers(orgID, ruleName)
	if err != nil {
		log.With("org", orgID, "rule", ruleName).Warnf("failed reading the hold's reviewer groups, reason=%v", err)
		return nil
	}
	return groups
}

// storedHoldReviewers is holdReviewersOf, returning a failed read.
func storedHoldReviewers(orgID uuid.UUID, ruleName string) ([]string, error) {
	if !appconfig.Get().IsControlPlane() {
		return nil, nil
	}
	return services.AnalyzerApprovalReviewers(models.DB, orgID, ruleName)
}
