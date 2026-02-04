package reviewapi

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

func validateAccessControlRuleBody(req *openapi.AccessControlRuleRequest) error {
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		return err
	}

	if len(req.ReviewersGroups) == 0 {
		return fmt.Errorf("reviewers_groups must have at least 1 entry")
	}

	return nil
}

// CreateAccessControlRule
//
//	@Summary		Create Access Control Rule
//	@Description	Create a new access control rule for the organization
//	@Tags			Access Control Rules
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.AccessControlRuleRequest	true	"The request body resource"
//	@Success		201				{object}	openapi.AccessControlRule
//	@Failure		400,422,500		{object}	openapi.HTTPError
//	@Router			/reviews/rules [post]
func CreateAccessControlRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.AccessControlRuleRequest
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

	if err := validateAccessControlRuleBody(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	accessControlRule := &models.AccessControlRules{
		OrgID:              orgID,
		Name:               req.Name,
		Description:        req.Description,
		ReviewersGroups:    req.ReviewersGroups,
		ForceApproveGroups: req.ForceApproveGroups,
		AccessMaxDuration:  req.AccessMaxDuration,
		MinApprovals:       req.MinApprovals,
	}

	if err := models.CreateAccessControlRules(models.DB, accessControlRule); err != nil {
		if err == gorm.ErrDuplicatedKey {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "access control rule with the same name already exists"})
			return
		}

		log.Errorf("failed to create access control rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to create access control rule"})
		return
	}

	c.JSON(http.StatusCreated, toAccessControlRuleOpenAPI(accessControlRule))
}

// GetAccessControlRule
//
//	@Summary		Get Access Control Rule
//	@Description	Get an access control rule by ID
//	@Tags			Access Control Rules
//	@Produce		json
//	@Param			name	path		string	true	"Access Control Rule Name"
//	@Success		200	{object}	openapi.AccessControlRule
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/reviews/rules/{name} [get]
func GetAccessControlRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	accessControlRule, err := models.GetAccessControlRulesByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access control rule not found"})
			return
		}
		log.Errorf("failed to get access control rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get access control rule"})
		return
	}

	c.JSON(http.StatusOK, toAccessControlRuleOpenAPI(accessControlRule))
}

// ListAccessControlRules
//
//	@Summary		List Access Control Rules
//	@Description	List all access control rules for the organization with pagination
//	@Tags			Access Control Rules
//	@Produce		json
//	@Param			page		query		int	false	"Page number (default: 1)"
//	@Param			page_size	query		int	false	"Page size (default: 0 for all)"
//	@Success		200	{object}	openapi.PaginatedResponse[openapi.AccessControlRule]
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/reviews/rules [get]
func ListAccessControlRules(c *gin.Context) {
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

	opts := models.AccessControlRulesFilterOption{
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

	accessControlRules, total, err := models.ListAccessControlRules(models.DB, orgID, opts)
	if err != nil {
		log.Errorf("failed to list access control rules: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to list access control rules"})
		return
	}

	var data []openapi.AccessControlRule
	for _, rule := range accessControlRules {
		data = append(data, *toAccessControlRuleOpenAPI(&rule))
	}

	if data == nil {
		data = []openapi.AccessControlRule{}
	}

	response := openapi.PaginatedResponse[openapi.AccessControlRule]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  opts.Page,
			Size:  opts.PageSize,
		},
		Data: data,
	}

	c.JSON(http.StatusOK, response)
}

// UpdateAccessControlRule
//
//	@Summary		Update Access Control Rule
//	@Description	Update an access control rule by ID
//	@Tags			Access Control Rules
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Access Control Rule ID"
//	@Param			request	body		openapi.AccessControlRuleRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.AccessControlRule
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/reviews/rules/{name} [put]
func UpdateAccessControlRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	var req openapi.AccessControlRuleRequest
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

	if err := validateAccessControlRuleBody(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// Check if access control rule exists
	existingRule, err := models.GetAccessControlRulesByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access control rule not found"})
			return
		}
		log.Errorf("failed to get access control rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get access control rule"})
		return
	}

	// Update fields
	existingRule.Name = req.Name
	existingRule.Description = req.Description
	existingRule.ReviewersGroups = req.ReviewersGroups
	existingRule.ForceApproveGroups = req.ForceApproveGroups
	existingRule.AccessMaxDuration = req.AccessMaxDuration
	existingRule.MinApprovals = req.MinApprovals

	if err := models.UpdateAccessControlRules(models.DB, existingRule); err != nil {
		log.Errorf("failed to update access control rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update access control rule"})
		return
	}

	c.JSON(http.StatusOK, toAccessControlRuleOpenAPI(existingRule))
}

// DeleteAccessControlRule
//
//	@Summary		Delete Access Control Rule
//	@Description	Delete an access control rule by Name
//	@Tags			Access Control Rules
//	@Produce		json
//	@Param			name	path		string	true	"Access Control Rule Name"
//	@Success		204	"No Content"
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/reviews/rules/{name} [delete]
func DeleteAccessControlRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	name := c.Param("name")

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed to parse org ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization ID"})
		return
	}

	err = models.DeleteAccessControlRulesByName(models.DB, name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "access control rule not found"})
			return
		}
		log.Errorf("failed to delete access control rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to delete access control rule"})
		return
	}

	c.Status(http.StatusNoContent)
}

func toAccessControlRuleOpenAPI(rule *models.AccessControlRules) *openapi.AccessControlRule {
	return &openapi.AccessControlRule{
		ID:                 rule.ID.String(),
		Name:               rule.Name,
		Description:        rule.Description,
		ReviewersGroups:    rule.ReviewersGroups,
		ForceApproveGroups: rule.ForceApproveGroups,
		AccessMaxDuration:  rule.AccessMaxDuration,
		MinApprovals:       rule.MinApprovals,
		CreatedAt:          rule.CreatedAt,
		UpdatedAt:          rule.UpdatedAt,
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
