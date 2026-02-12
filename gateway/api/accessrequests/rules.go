package accessrequests

import (
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

func validateAccessRequestRuleBody(req *openapi.AccessRequestRuleRequest, foundRule *models.AccessRequestRule) error {
	if foundRule != nil {
		return fmt.Errorf("an access request rule with the same connection names and access type already exists")
	}

	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		return err
	}

	if len(req.ConnectionNames) == 0 {
		return fmt.Errorf("connection_names must have at least 1 entry")
	}

	if req.AccessType != "jit" && req.AccessType != "command" {
		return fmt.Errorf("access_type must be either 'jit' or 'command'")
	}

	if len(req.ReviewersGroups) == 0 {
		return fmt.Errorf("reviewers_groups must have at least 1 entry")
	}

	if !req.AllGroupsMustApprove && (req.MinApprovals == nil || *req.MinApprovals < 1) {
		return fmt.Errorf("min_approvals must be at least 1 when all_groups_must_approve is false")
	}

	return nil
}

// CreateAccessRequestRule
//
//	@Summary		Create Access Request Rule
//	@Description	Create a new access request rule for the organization
//	@Tags			Access Request Rules
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.AccessRequestRuleRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.AccessRequestRule
//	@Failure		400,422,500		{object}	openapi.HTTPError
//	@Router			/access-requests/rules [post]
func CreateAccessRequestRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.AccessRequestRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	foundRule, err := models.GetAccessRequestRuleByResourceNamesAndAccessType(models.DB, orgID, req.ConnectionNames, req.AccessType)
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Errorf("failed to check existing access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to create access request rule"})
		return
	}

	if err := validateAccessRequestRuleBody(&req, foundRule); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	accessRequestRule := &models.AccessRequestRule{
		OrgID:                  orgID,
		Name:                   req.Name,
		Description:            req.Description,
		AccessType:             req.AccessType,
		ConnectionNames:        req.ConnectionNames,
		ApprovalRequiredGroups: req.ApprovalRequiredGroups,
		AllGroupsMustApprove:   req.AllGroupsMustApprove,
		ReviewersGroups:        req.ReviewersGroups,
		ForceApprovalGroups:    req.ForceApprovalGroups,
		AccessMaxDuration:      req.AccessMaxDuration,
		MinApprovals:           req.MinApprovals,
	}

	if err := models.CreateAccessRequestRule(models.DB, accessRequestRule); err != nil {
		if err == gorm.ErrDuplicatedKey {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "access request rule with the same name already exists"})
			return
		}

		log.Errorf("failed to create access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to create access request rule"})
		return
	}

	c.JSON(http.StatusCreated, toAccessRequestRuleOpenApi(accessRequestRule))
}

// GetAccessRequestRule
//
//	@Summary		Get Access Request Rule
//	@Description	Get an access request rule by ID
//	@Tags			Access Request Rules
//	@Produce		json
//	@Param			name	path		string	true	"Access request rule Name"
//	@Success		200	{object}	openapi.AccessRequestRule
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/access-requests/rules/{name} [get]
func GetAccessRequestRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	accessRequestRule, err := models.GetAccessRequestRuleByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access request rule not found"})
			return
		}
		log.Errorf("failed to get access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get access request rule"})
		return
	}

	c.JSON(http.StatusOK, toAccessRequestRuleOpenApi(accessRequestRule))
}

// ListAccessRequestRules
//
//	@Summary		List Access Request Rules
//	@Description	List all access request rules for the organization with pagination
//	@Tags			Access Request Rules
//	@Produce		json
//	@Param			page		query		int	false	"Page number (default: 1)"
//	@Param			page_size	query		int	false	"Page size (default: 0 for all)"
//	@Success		200	{object}	openapi.PaginatedResponse[openapi.AccessRequestRule]
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/access-requests/rules [get]
func ListAccessRequestRules(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	// Parse pagination parameters
	queryParams := c.Request.URL.Query()
	pageStr := queryParams.Get("page")
	pageSizeStr := queryParams.Get("page_size")

	opts := models.AccessRequestRulesFilterOption{
		Page:     1,
		PageSize: 0, // 0 means no pagination, return all
	}

	if pageStr != "" {
		if page, parseErr := parseIntParam(pageStr, "page"); parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": parseErr.Error()})
			return
		} else {
			opts.Page = page
		}
	}

	if pageSizeStr != "" {
		if pageSize, parseErr := parseIntParam(pageSizeStr, "page_size"); parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": parseErr.Error()})
			return
		} else {
			opts.PageSize = pageSize
		}
	}

	accessRequestRules, total, err := models.ListAccessRequestRules(models.DB, orgID, opts)
	if err != nil {
		log.Errorf("failed to list access request rules: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to list access request rules"})
		return
	}

	var data []openapi.AccessRequestRule
	for _, rule := range accessRequestRules {
		data = append(data, *toAccessRequestRuleOpenApi(&rule))
	}

	if data == nil {
		data = []openapi.AccessRequestRule{}
	}

	response := openapi.PaginatedResponse[openapi.AccessRequestRule]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  opts.Page,
			Size:  opts.PageSize,
		},
		Data: data,
	}

	c.JSON(http.StatusOK, response)
}

// UpdateAccessRequestRule
//
//	@Summary		Update Access Request Rule
//	@Description	Update an access request rule by ID
//	@Tags			Access Request Rules
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Access request Rule ID"
//	@Param			request	body		openapi.AccessRequestRuleRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.AccessRequestRule
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/access-requests/rules/{name} [put]
func UpdateAccessRequestRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	var req openapi.AccessRequestRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	// Check if another access request rule with the same connection names and access type exists
	foundRule, err := models.GetAccessRequestRuleByResourceNamesAndAccessType(models.DB, orgID, req.ConnectionNames, req.AccessType)
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Errorf("failed to check existing access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to create access request rule"})
		return
	}

	// Check if access request rule exists
	existingRule, err := models.GetAccessRequestRuleByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access request rule not found"})
			return
		}
		log.Errorf("failed to get access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get access request rule"})
		return
	}

	if foundRule != nil && foundRule.ID != existingRule.ID {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "an access request rule with the same connection names and access type already exists"})
		return
	}

	if err := validateAccessRequestRuleBody(&req, nil); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// Update fields
	existingRule.Name = req.Name
	existingRule.Description = req.Description
	existingRule.AccessType = req.AccessType
	existingRule.ConnectionNames = req.ConnectionNames
	existingRule.ApprovalRequiredGroups = req.ApprovalRequiredGroups
	existingRule.AllGroupsMustApprove = req.AllGroupsMustApprove
	existingRule.ReviewersGroups = req.ReviewersGroups
	existingRule.ForceApprovalGroups = req.ForceApprovalGroups
	existingRule.AccessMaxDuration = req.AccessMaxDuration
	existingRule.MinApprovals = req.MinApprovals

	if err := models.UpdateAccessRequestRule(models.DB, existingRule); err != nil {
		log.Errorf("failed to update access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update access request rule"})
		return
	}

	c.JSON(http.StatusOK, toAccessRequestRuleOpenApi(existingRule))
}

// DeleteAccessRequestRule
//
//	@Summary		Delete Access Request Rule
//	@Description	Delete an access request rule by name
//	@Tags			Access Request Rules
//	@Produce		json
//	@Param			name	path		string	true	"Access request rule name"
//	@Success		204	"No Content"
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/access-requests/rules/{name} [delete]
func DeleteAccessRequestRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	err = models.DeleteAccessRequestRuleByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access request rule not found"})
			return
		}
		log.Errorf("failed to delete access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to delete access request rule"})
		return
	}

	c.Status(http.StatusNoContent)
}

func toAccessRequestRuleOpenApi(rule *models.AccessRequestRule) *openapi.AccessRequestRule {
	return &openapi.AccessRequestRule{
		ID:                     rule.ID.String(),
		Name:                   rule.Name,
		Description:            rule.Description,
		AccessType:             rule.AccessType,
		ConnectionNames:        rule.ConnectionNames,
		ApprovalRequiredGroups: rule.ApprovalRequiredGroups,
		ReviewersGroups:        rule.ReviewersGroups,
		AllGroupsMustApprove:   rule.AllGroupsMustApprove,
		ForceApprovalGroups:    rule.ForceApprovalGroups,
		AccessMaxDuration:      rule.AccessMaxDuration,
		MinApprovals:           rule.MinApprovals,
		CreatedAt:              rule.CreatedAt,
		UpdatedAt:              rule.UpdatedAt,
	}
}

func parseIntParam(value, paramName string) (int, error) {
	var result int
	if _, err := fmt.Sscanf(value, "%d", &result); err != nil {
		return 0, fmt.Errorf("invalid %s parameter", paramName)
	}
	if result < 0 {
		return 0, fmt.Errorf("%s must be non-negative", paramName)
	}
	return result, nil
}
