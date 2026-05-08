package apiattributes

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

const (
	rulepackFlagName       = "experimental.rulepacks"
	rulepackAttrNamePrefix = "rulepack_"
)

func validateRulepackPrefix(req openapi.AttributeRequest) error {
	hasPrefix := strings.HasPrefix(req.Name, rulepackAttrNamePrefix)
	hasRulepackID := req.RulepackID != nil && *req.RulepackID != ""
	if hasRulepackID && !hasPrefix {
		return errPrefixMismatch
	}
	if hasPrefix && !hasRulepackID {
		return errMissingRulepackID
	}
	return nil
}

var (
	errPrefixMismatch    = &validationError{msg: "attribute name must start with `rulepack_` when rulepack_id is set"}
	errMissingRulepackID = &validationError{msg: "attribute names with the `rulepack_` prefix must include a rulepack_id"}
)

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func guardManagedAttribute(c *gin.Context, attr *models.Attribute, orgID uuid.UUID) bool {
	if attr.RulepackID == nil {
		return false
	}
	rp, err := models.GetRulepack(models.DB, orgID, *attr.RulepackID)
	if err != nil {
		return false
	}
	if rp.IsManaged {
		c.JSON(http.StatusForbidden, gin.H{"message": "attributes belonging to managed rulepacks cannot be modified"})
		return true
	}
	return false
}

func buildAttributeModel(orgID uuid.UUID, req openapi.AttributeRequest) *models.Attribute {
	var connAttrs []models.ConnectionAttribute
	if req.ConnectionNames != nil {
		connAttrs = make([]models.ConnectionAttribute, len(req.ConnectionNames))

		for i, connName := range req.ConnectionNames {
			connAttrs[i] = models.ConnectionAttribute{OrgID: orgID, AttributeName: req.Name, ConnectionName: connName}
		}
	}

	var arrAttrs []models.AccessRequestRuleAttribute
	if req.AccessRequestRuleNames != nil {
		arrAttrs = make([]models.AccessRequestRuleAttribute, len(req.AccessRequestRuleNames))

		for i, arrName := range req.AccessRequestRuleNames {
			arrAttrs[i] = models.AccessRequestRuleAttribute{OrgID: orgID, AttributeName: req.Name, AccessRuleName: arrName}
		}
	}

	var grAttrs []models.GuardrailRuleAttribute
	if req.GuardrailRuleNames != nil {
		grAttrs = make([]models.GuardrailRuleAttribute, len(req.GuardrailRuleNames))

		for i, grName := range req.GuardrailRuleNames {
			grAttrs[i] = models.GuardrailRuleAttribute{OrgID: orgID, AttributeName: req.Name, GuardrailRuleName: grName}
		}
	}

	var dmAttrs []models.DatamaskingRuleAttribute
	if req.DatamaskingRuleNames != nil {
		dmAttrs = make([]models.DatamaskingRuleAttribute, len(req.DatamaskingRuleNames))

		for i, dmName := range req.DatamaskingRuleNames {
			dmAttrs[i] = models.DatamaskingRuleAttribute{OrgID: orgID, AttributeName: req.Name, DatamaskingRuleName: dmName}
		}
	}

	var acgAttrs []models.AccessControlGroupAttribute
	if req.AccessControlGroupNames != nil {
		acgAttrs = make([]models.AccessControlGroupAttribute, len(req.AccessControlGroupNames))

		for i, groupName := range req.AccessControlGroupNames {
			acgAttrs[i] = models.AccessControlGroupAttribute{OrgID: orgID, AttributeName: req.Name, GroupName: groupName}
		}
	}

	var rulepackID *uuid.UUID
	if req.RulepackID != nil && *req.RulepackID != "" {
		if parsed, err := uuid.Parse(*req.RulepackID); err == nil {
			rulepackID = &parsed
		}
	}

	return &models.Attribute{
		OrgID:               orgID,
		Name:                req.Name,
		Description:         req.Description,
		RulepackID:          rulepackID,
		Connections:         connAttrs,
		AccessRequestRules:  arrAttrs,
		GuardrailRules:      grAttrs,
		DatamaskingRules:    dmAttrs,
		AccessControlGroups: acgAttrs,
	}
}

// CreateAttribute
//
//	@Summary		Create Attribute
//	@Description	Create a new attribute for the organization.
//	@Tags			Attributes
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.AttributeRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.Attributes
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/attributes [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	rulepackEnabled := featureflag.IsEnabled(ctx.GetOrgID(), rulepackFlagName)
	if rulepackEnabled {
		if vErr := validateRulepackPrefix(req); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
			return
		}
		if vErr := assertRulepackUsable(orgID, req.RulepackID); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
			return
		}
	} else {
		req.RulepackID = nil
	}

	attr := buildAttributeModel(orgID, req)

	err = models.UpsertAttribute(models.DB, attr)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		reconcileMachineIdentitiesForAttribute(ctx.GetOrgID(), attr)
		c.JSON(http.StatusCreated, toResponse(attr))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating attribute: %v", err)
	}
}

func assertRulepackUsable(orgID uuid.UUID, rulepackIDStr *string) error {
	if rulepackIDStr == nil || *rulepackIDStr == "" {
		return nil
	}
	id, err := uuid.Parse(*rulepackIDStr)
	if err != nil {
		return &validationError{msg: "invalid rulepack_id"}
	}
	rp, err := models.GetRulepack(models.DB, orgID, id)
	if err != nil {
		return &validationError{msg: "rulepack_id does not reference an existing rulepack"}
	}
	if rp.IsManaged {
		return &validationError{msg: "managed rulepacks cannot accept new or modified attributes"}
	}
	return nil
}

// UpdateAttribute
//
//	@Summary		Update Attribute
//	@Description	Rename an existing attribute.
//	@Tags			Attributes
//	@Accept			json
//	@Produce		json
//	@Param			name		path		string						true	"Name of the attribute"
//	@Param			request		body		openapi.AttributeRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.Attributes
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/attributes/{name} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	name := c.Param("name")
	existentAttr, err := models.GetAttribute(models.DB, orgID, name)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}

		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching attribute: %v", err)
		return
	}

	rulepackEnabled := featureflag.IsEnabled(ctx.GetOrgID(), rulepackFlagName)
	if rulepackEnabled {
		if guardManagedAttribute(c, existentAttr, orgID) {
			return
		}
		if vErr := validateRulepackPrefix(req); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
			return
		}
		if vErr := assertRulepackUsable(orgID, req.RulepackID); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
			return
		}
	} else {
		req.RulepackID = nil
	}

	attr := buildAttributeModel(orgID, req)
	attr.ID = existentAttr.ID
	if !rulepackEnabled {
		attr.RulepackID = existentAttr.RulepackID
	}

	err = models.UpsertAttribute(models.DB, attr)

	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		reconcileMachineIdentitiesForAttribute(ctx.GetOrgID(), attr)
		c.JSON(http.StatusOK, toResponse(attr))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating attribute: %v", err)
	}
}

// DeleteAttribute
//
//	@Summary		Delete Attribute
//	@Description	Delete an attribute by name or ID.
//	@Tags			Attributes
//	@Produce		json
//	@Param			name		path	string	true	"Name of the attribute"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/attributes/{name} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	if featureflag.IsEnabled(ctx.GetOrgID(), rulepackFlagName) {
		existentAttr, gerr := models.GetAttribute(models.DB, orgID, c.Param("name"))
		if gerr == nil {
			if guardManagedAttribute(c, existentAttr, orgID) {
				return
			}
		}
	}

	err = models.DeleteAttribute(models.DB, orgID, c.Param("name"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting attribute: %v", err)
	}
}

// ListAttributes
//
//	@Summary		List Attributes
//	@Description	List attributes for the organization with optional pagination and search.
//	@Tags			Attributes
//	@Produce		json
//	@Param			search		query		string	false	"Search by name"
//	@Param			page		query		int		false	"Page number (1-based)"
//	@Param			page_size	query		int		false	"Items per page (1-100)"
//	@Success		200			{object}	openapi.PaginatedResponse[openapi.Attributes]
//	@Failure		422,500		{object}	openapi.HTTPError
//	@Router			/attributes [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

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

	opts := models.AttributeFilterOption{
		Search:   q.Get("search"),
		Page:     page,
		PageSize: pageSize,
	}

	attrs, total, err := models.ListAttributes(models.DB, orgID, opts)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing attributes: %v", err)
		return
	}

	data := make([]openapi.Attributes, 0, len(attrs))
	for _, a := range attrs {
		data = append(data, toResponse(a))
	}

	c.JSON(http.StatusOK, openapi.PaginatedResponse[openapi.Attributes]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  page,
			Size:  pageSize,
		},
		Data: data,
	})
}

// GetAttribute
//
//	@Summary		Get Attribute
//	@Description	Get an attribute by name or ID.
//	@Tags			Attributes
//	@Produce		json
//	@Param			name		path		string	true	"Name of the attribute"
//	@Success		200			{object}	openapi.Attributes
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/attributes/{name} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid org id")
		return
	}

	attr, err := models.GetAttribute(models.DB, orgID, c.Param("name"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toResponse(attr))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching attribute: %v", err)
	}
}

func toResponse(a *models.Attribute) openapi.Attributes {
	connections := make([]string, len(a.Connections))
	for i, ca := range a.Connections {
		connections[i] = ca.ConnectionName
	}

	accessRequest := make([]string, len(a.AccessRequestRules))
	for i, arr := range a.AccessRequestRules {
		accessRequest[i] = arr.AccessRuleName
	}

	guardrail := make([]string, len(a.GuardrailRules))
	for i, gr := range a.GuardrailRules {
		guardrail[i] = gr.GuardrailRuleName
	}

	datamasking := make([]string, len(a.DatamaskingRules))
	for i, dm := range a.DatamaskingRules {
		datamasking[i] = dm.DatamaskingRuleName
	}

	accessControlGroups := make([]string, len(a.AccessControlGroups))
	for i, acg := range a.AccessControlGroups {
		accessControlGroups[i] = acg.GroupName
	}

	var rulepackID *string
	if a.RulepackID != nil {
		s := a.RulepackID.String()
		rulepackID = &s
	}

	return openapi.Attributes{
		ID:                      a.ID.String(),
		OrgID:                   a.OrgID.String(),
		Name:                    a.Name,
		Description:             a.Description,
		RulepackID:              rulepackID,
		ConnectionNames:         connections,
		AccessRequestRuleNames:  accessRequest,
		GuardrailRuleNames:      guardrail,
		DatamaskingRuleNames:    datamasking,
		AccessControlGroupNames: accessControlGroups,
		CreatedAt:               a.CreatedAt,
	}
}

// reconcileMachineIdentitiesForAttribute triggers credential reconciliation for machine
// identities affected by a change to an attribute's connections or MI associations.
func reconcileMachineIdentitiesForAttribute(orgID string, attr *models.Attribute) {
	ctx := context.Background()

	// If connection associations changed, reconcile MIs for each connection in the attribute
	if attr.Connections != nil {
		for _, ca := range attr.Connections {
			if err := services.ReconcileAllMachineIdentitiesForConnection(ctx, orgID, ca.ConnectionName); err != nil {
				log.Warnf("failed reconciling MI credentials after attribute %s changed connection %s: %v", attr.Name, ca.ConnectionName, err)
			}
		}
	}

	// If MI associations changed, reconcile each affected MI directly
	if attr.MachineIdentities != nil {
		for _, mia := range attr.MachineIdentities {
			if err := services.ReconcileMachineIdentityCredentials(ctx, orgID, mia.MachineIdentityName); err != nil {
				log.Warnf("failed reconciling MI %s after attribute %s change: %v", mia.MachineIdentityName, attr.Name, err)
			}
		}
	}

	// Also reconcile MIs that previously had this attribute but were removed (they
	// may need credentials revoked). Query all MIs that currently have a credential
	// for connections in this attribute.
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		return
	}
	miNames, err := models.GetMachineIdentityNamesMatchingAttributes(models.DB, orgUUID, []string{attr.Name})
	if err != nil {
		log.Warnf("failed fetching MIs matching attribute %s: %v", attr.Name, err)
		return
	}
	for _, miName := range miNames {
		if err := services.ReconcileMachineIdentityCredentials(ctx, orgID, miName); err != nil {
			log.Warnf("failed reconciling MI %s after attribute %s change: %v", miName, attr.Name, err)
		}
	}
}
