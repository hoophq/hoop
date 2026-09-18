package apiguardrails

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func getLicenseType(ctx *storagev2.Context) string {
	licenseType := license.OSSType
	if ctx.OrgLicenseData != nil && len(*ctx.OrgLicenseData) > 0 {
		var l license.License
		if err := json.Unmarshal(*ctx.OrgLicenseData, &l); err == nil {
			licenseType = l.Payload.Type
		}
	}
	return licenseType
}

func countRules(ruleMap map[string]any) int {
	if ruleMap == nil {
		return 0
	}
	rules, ok := ruleMap["rules"]
	if !ok {
		return 0
	}
	list, ok := rules.([]interface{})
	if !ok {
		return 0
	}
	return len(list)
}

func validateOssRulesLimitations(req *openapi.GuardRailRuleRequest) error {
	if countRules(req.Input) > 1 {
		return fmt.Errorf("input rules are limited to 1 rule in OSS version")
	}
	if countRules(req.Output) > 1 {
		return fmt.Errorf("output rules are limited to 1 rule in OSS version")
	}
	return nil
}

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

	if getLicenseType(ctx) == license.OSSType {
		rules, err := models.ListGuardRailRules(ctx.GetOrgID(), models.GuardRailListOption{
			IncludeAllRulepackOwned: false,
		})
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing guardrail rules: %v", err)
			return
		}
		if len(rules) >= 1 {
			c.JSON(http.StatusForbidden, gin.H{"message": "guardrail rules are limited to 1 rule in OSS version"})
			return
		}
		if err := validateOssRulesLimitations(req); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
			return
		}
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

	if refuseSidecarTargets(c, ctx, req, req.Name) {
		return
	}

	err := models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, true)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	case nil:
		if err := upsertGuardrailRuleAttributes(ctx, rule.Name, req.Attributes); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed upserting guard rail rule attributes: %v", err)
			return
		}
		if err := persistSidecarTargets(ctx, rule.Name, req.SidecarTargets); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed binding the rule to its sidecars: %v", err)
			return
		}
		c.JSON(http.StatusCreated, &openapi.GuardRailRuleResponse{
			ID:             rule.ID,
			Name:           rule.Name,
			Description:    rule.Description,
			Input:          rule.Input,
			Output:         rule.Output,
			ConnectionIDs:  rule.ConnectionIDs,
			Attributes:     req.Attributes,
			SidecarTargets: loadSidecarTargets(ctx.GetOrgID(), rule.Name),
			CreatedAt:      rule.CreatedAt,
			UpdatedAt:      rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed creating guard rail rule: %v", err)
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

	ruleID := c.Param("id")
	existing, err := models.GetGuardRailRules(ctx.GetOrgID(), ruleID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	case nil:
		if existing.ManagedBy != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is managed by Hoop and cannot be modified directly"})
			return
		}
		if featureflag.IsEnabled(ctx.GetOrgID(), services.RulepackFlagName) && existing.RulepackID.Valid {
			c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is owned by a rulepack and cannot be modified directly"})
			return
		}
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching guardrail rule: %v", err)
		return
	}

	if getLicenseType(ctx) == license.OSSType {
		if err := validateOssRulesLimitations(req); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
			return
		}
	}

	// Filter out empty connection IDs
	validConnectionIDs := filterEmptyIDs(req.ConnectionIDs)

	rule := &models.GuardRailRules{
		OrgID:       ctx.GetOrgID(),
		ID:          ruleID,
		Name:        req.Name,
		Description: req.Description,
		Input:       req.Input,
		Output:      req.Output,
		UpdatedAt:   time.Now().UTC(),
	}

	if refuseSidecarTargets(c, ctx, req, existing.Name) {
		return
	}

	// Update guardrail and associate connections in a single transaction
	err = models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, false)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	case nil:
		if err := upsertGuardrailRuleAttributes(ctx, rule.Name, req.Attributes); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed upserting guard rail rule attributes: %v", err)
			return
		}
		if err := persistSidecarTargets(ctx, rule.Name, req.SidecarTargets); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed binding the rule to its sidecars: %v", err)
			return
		}
		c.JSON(http.StatusOK, &openapi.GuardRailRuleResponse{
			ID:             rule.ID,
			Name:           rule.Name,
			Description:    rule.Description,
			Input:          rule.Input,
			Output:         rule.Output,
			ConnectionIDs:  rule.ConnectionIDs,
			Attributes:     req.Attributes,
			SidecarTargets: loadSidecarTargets(ctx.GetOrgID(), rule.Name),
			CreatedAt:      rule.CreatedAt,
			UpdatedAt:      rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "Failed updating guard rail rule: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing guard rail rules: %v", err)
		return
	}

	rules := []openapi.GuardRailRuleResponse{}
	for _, rule := range ruleList {
		rules = append(rules, openapi.GuardRailRuleResponse{
			ID:            rule.ID,
			Name:          rule.Name,
			Description:   rule.Description,
			ManagedBy:     rule.ManagedBy,
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
			ManagedBy:     rule.ManagedBy,
			Input:         rule.Input,
			Output:        rule.Output,
			ConnectionIDs: rule.ConnectionIDs,
			Attributes:    rule.Attributes,
			// Read back on the single-rule route, which is what the edit form
			// loads. Without it the form opens with the picker empty and the
			// next save unbinds the rule from every sidecar it reached.
			SidecarTargets: loadSidecarTargets(ctx.GetOrgID(), rule.Name),
			CreatedAt:      rule.CreatedAt,
			UpdatedAt:      rule.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing guard rail rules: %v", err)
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

	existing, gerr := models.GetGuardRailRules(ctx.GetOrgID(), ruleID)
	switch gerr {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	case nil:
		if existing.ManagedBy != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is managed by Hoop and cannot be deleted directly"})
			return
		}
		if featureflag.IsEnabled(ctx.GetOrgID(), services.RulepackFlagName) && existing.RulepackID.Valid {
			c.JSON(http.StatusBadRequest, gin.H{"message": "this rule is owned by a rulepack and cannot be deleted directly"})
			return
		}
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, gerr, "failed fetching guardrail rule: %v", gerr)
		return
	}

	err := models.DeleteGuardRailRules(ctx.GetOrgID(), ruleID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed removing guard rail rules: %v", err)
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

// refuseSidecarTargets validates a rule against what a sidecar can enforce and
// reports whether it answered the request.
//
// Two checks, and both exist because the alternative is a rule that looks saved
// and enforces nothing. The vocabulary check refuses a rule shape no sidecar
// configuration can express; the target check refuses a binding that does not
// resolve to exactly one listener on a sidecar this organization owns.
//
// It runs on every write to a rule that is bound, not only when the binding is
// created: editing a compliant rule into a non-compliant one would otherwise
// walk straight past it.
// storedName is the rule's name as persisted right now, which a rename makes
// different from req.Name: the bindings still sit under the old one until the
// write cascades them, so both are looked up.
func refuseSidecarTargets(c *gin.Context, ctx *storagev2.Context, req *openapi.GuardRailRuleRequest, storedName string) bool {
	orgID := uuid.MustParse(ctx.GetOrgID())
	targets := toSidecarTargets(derefTargets(req.SidecarTargets))

	bound := len(targets) > 0
	for _, name := range []string{req.Name, storedName} {
		if bound {
			break
		}
		// Already bound elsewhere: the rule's content still has to stay
		// enforceable, even when this request does not mention the bindings.
		existing, err := models.SidecarsBoundToGuardrailRule(models.DB, orgID, name)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the rule's sidecar bindings")
			return true
		}
		bound = len(existing) > 0
	}
	if !bound {
		return false
	}

	input, _ := json.Marshal(req.Input)
	output, _ := json.Marshal(req.Output)
	if err := services.ValidateGuardrailRuleForSidecar(req.Name, input, output); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	if err := services.ValidateSidecarRuleTargets(models.DB, ctx.GetOrgID(), targets); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	return false
}

// persistSidecarTargets replaces the rule's target set. Called only after the
// rule row exists, so the junction's foreign key has something to point at.
//
// An ABSENT field is not an empty one: it leaves the bindings alone, so a write
// that says nothing about sidecars changes nothing about them. An explicit []
// is the admin unbinding the rule, and does replace the set with nothing.
func persistSidecarTargets(ctx *storagev2.Context, ruleName string, targets *[]openapi.SidecarRuleTarget) error {
	if targets == nil {
		return nil
	}
	return models.SetGuardrailRuleListeners(models.DB, uuid.MustParse(ctx.GetOrgID()),
		ruleName, toSidecarTargets(*targets))
}

// derefTargets reads the optional field as a list, for the guards, which treat
// "not mentioned" and "none" the same: neither adds a binding, and a rule that
// is bound elsewhere is checked either way.
func derefTargets(in *[]openapi.SidecarRuleTarget) []openapi.SidecarRuleTarget {
	if in == nil {
		return nil
	}
	return *in
}

func toSidecarTargets(in []openapi.SidecarRuleTarget) []models.SidecarRuleTarget {
	out := make([]models.SidecarRuleTarget, 0, len(in))
	for _, t := range in {
		if t.SidecarID == "" {
			continue
		}
		out = append(out, models.SidecarRuleTarget{SidecarID: t.SidecarID, ListenerName: t.ListenerName})
	}
	return out
}

// loadSidecarTargets renders back where a rule is bound, so a round-trip
// through the API returns what was saved.
func loadSidecarTargets(orgID, ruleName string) []openapi.SidecarRuleTarget {
	rows, err := models.ListGuardrailRuleTargets(models.DB, uuid.MustParse(orgID), ruleName)
	if err != nil {
		log.Warnf("failed reading the sidecar targets of guardrail rule %q, reason=%v", ruleName, err)
		return nil
	}
	out := make([]openapi.SidecarRuleTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, openapi.SidecarRuleTarget{SidecarID: r.SidecarID, ListenerName: r.ListenerName})
	}
	return out
}
