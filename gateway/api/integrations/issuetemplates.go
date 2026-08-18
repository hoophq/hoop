package apijiraintegration

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const connectionTagValuePrefix = "session.connection_tags."

func countItems(m map[string]any) int {
	if m == nil {
		return 0
	}
	items, ok := m["items"].([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func countMappingItemsByKind(m map[string]any) (tagCount, nonTagCount int) {
	if m == nil {
		return 0, 0
	}
	items, ok := m["items"].([]any)
	if !ok {
		return 0, 0
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, _ := item["value"].(string)
		if strings.HasPrefix(value, connectionTagValuePrefix) {
			tagCount++
		} else {
			nonTagCount++
		}
	}
	return tagCount, nonTagCount
}

func validateOssTemplateLimits(req *openapi.JiraIssueTemplateRequest) error {
	tagCount, nonTagCount := countMappingItemsByKind(req.MappingTypes)
	if tagCount > 1 {
		return fmt.Errorf("preset mapping rules are limited to 1 in the Free plan")
	}
	if nonTagCount > 1 {
		return fmt.Errorf("custom mapping rules are limited to 1 in the Free plan")
	}
	if countItems(req.PromptTypes) > 1 {
		return fmt.Errorf("prompt rules are limited to 1 in the Free plan")
	}
	if countItems(req.CmdbTypes) > 1 {
		return fmt.Errorf("CMDB rules are limited to 1 in the Free plan")
	}
	return nil
}

// ListIssueTemplates
//
//	@Summary		List Issue Templates
//	@Description	List Issue Templates
//	@Tags			Jira
//	@Produce		json
//	@Success		200		{array}		openapi.JiraIssueTemplate
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/issuetemplates [get]
func ListIssueTemplates(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	issueList, err := models.ListJiraIssueTemplates(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing issue templates: %v", err)
		return
	}

	issues := []openapi.JiraIssueTemplate{}
	for _, issue := range issueList {
		issues = append(issues, openapi.JiraIssueTemplate{
			ID:                              issue.ID,
			Name:                            issue.Name,
			Description:                     issue.Description,
			ProjectKey:                      issue.ProjectKey,
			RequestTypeID:                   issue.RequestTypeID,
			IssueTransitionNameOnClose:      issue.IssueTransitionNameOnClose,
			SkipTransitionOnNonZeroExitCode: issue.SkipTransitionOnNonZeroExitCode,
			MappingTypes:                    issue.MappingTypes,
			PromptTypes:                     issue.PromptTypes,
			CmdbTypes:                       issue.CmdbTypes,
			ConnectionIDs:                   issue.ConnectionIDs,
			CreatedAt:                       issue.CreatedAt,
			UpdatedAt:                       issue.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, issues)
}

// GetIssueTemplates
//
//	@Summary		Get Issue Templates
//	@Description	Get Issue Templates
//	@Tags			Jira
//	@Produce		json
//	@Param			id		path		string	true	"The id of the resource"
//	@Success		200		{object}	openapi.JiraIssueTemplate
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/issuetemplates/{id} [get]
func GetIssueTemplatesByID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	issue, _, err := models.GetJiraIssueTemplatesByID(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:                              issue.ID,
			Name:                            issue.Name,
			Description:                     issue.Description,
			ProjectKey:                      issue.ProjectKey,
			RequestTypeID:                   issue.RequestTypeID,
			IssueTransitionNameOnClose:      issue.IssueTransitionNameOnClose,
			SkipTransitionOnNonZeroExitCode: issue.SkipTransitionOnNonZeroExitCode,
			MappingTypes:                    issue.MappingTypes,
			PromptTypes:                     issue.PromptTypes,
			CmdbTypes:                       issue.CmdbTypes,
			ConnectionIDs:                   issue.ConnectionIDs,
			CreatedAt:                       issue.CreatedAt,
			UpdatedAt:                       issue.UpdatedAt,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing issue templates: %v", err)
	}
}

// GetAssetObjects
//
//	@Summary		Get Asset Objects
//	@Description	Get objects from the Jira Service Management (JSM) Assets API
//	@Tags			Jira
//	@Produce		json
//	@Param			object_type_id		query		string	true	"The Jira object type to filter values for"
//	@Param			object_schema_id	query		string	false	"The Jira object schema id to fetch values for"
//	@Param			name				query		string	false	"Specify a name to filter"
//	@Param			aql					query		string	false	"AQL expression scoping the values; replaces the object_type_id filter when set"
//	@Success		200					{object}	openapi.JiraAssetObjects
//	@Failure		400,404,422,500		{object}	openapi.HTTPError
//	@Router			/integrations/jira/assets/objects [get]
func GetAssetObjects(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	objectTypeID, objectSchemaID, limit, offset, err := parseObjectValuesOptions(c)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	config, err := models.GetJiraIntegration(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining jira integration configuration: %v", err)
		return
	}
	query := buildAssetObjectsQuery(objectTypeID, objectSchemaID, c.Query("name"), c.Query("aql"))

	resp, err := jira.FetchObjectsByAQL(config, limit, offset, query)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching object type values from Jira: %v", err)
		return
	}
	log.Infof("jira assets api response, query=%q, islast=%v, total=%v/%v",
		query, resp.Last, resp.TotalCount, resp.Total)

	objectValues := []openapi.JiraAssetObjectValue{}
	for _, val := range resp.Values {
		objectValues = append(objectValues, openapi.JiraAssetObjectValue{
			ID:   val.GlobalID,
			Name: val.Name,
		})
	}
	c.JSON(http.StatusOK, openapi.JiraAssetObjects{
		Total:       resp.TotalCount,
		HasNextPage: !resp.Last,
		Values:      objectValues,
	})
}

// GetAssetFieldConfigs
//
//	@Summary		Get Asset Field Configurations
//	@Description	Get the AQL configuration of Jira Service Management (JSM) Assets custom fields. These are the object scope and dependent-field (issue scope) filters Jira's own portal applies; they drive the CMDB dropdown cascade. Fields without any configured filter are omitted.
//	@Tags			Jira
//	@Produce		json
//	@Param			jira_fields	query		string	true	"Comma-separated list of Jira custom field ids (e.g. customfield_10092)"
//	@Success		200			{object}	openapi.JiraAssetFieldConfigs
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/assets/fieldconfigs [get]
func GetAssetFieldConfigs(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	fieldIDs, err := parseIDListParam(c.Query("jira_fields"), "jira_fields")
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	config, err := models.GetJiraIntegration(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining jira integration configuration: %v", err)
		return
	}
	fieldConfigs, err := jira.FetchAssetFieldConfigs(config, fieldIDs)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching asset field configurations from Jira: %v", err)
		return
	}
	items := []openapi.JiraAssetFieldConfig{}
	for _, fc := range fieldConfigs {
		items = append(items, openapi.JiraAssetFieldConfig{
			JiraField:             fc.JiraField,
			ObjectSchemaID:        fc.ObjectSchemaID,
			ObjectFilterQuery:     fc.ObjectFilterQuery,
			IssueScopeFilterQuery: fc.IssueScopeFilterQuery,
		})
	}
	c.JSON(http.StatusOK, openapi.JiraAssetFieldConfigs{Items: items})
}

// parseIDListParam splits a comma-separated id list, trimming blanks and
// dropping duplicates so repeated ids cannot multiply upstream requests.
func parseIDListParam(raw, param string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s query string is required", param)
	}
	return ids, nil
}

// buildAssetObjectsQuery composes the AQL expression used to list asset
// objects.
//
// Without an aql expression the objects are scoped by the template's
// objectTypeId. An aql expression comes from the Assets field configuration
// in Jira, which already defines every object the field accepts, so it
// replaces the objectTypeId scope instead of narrowing it — exactly what
// Jira's own picker does. AND-ing both would return nothing whenever the
// template's object type disagrees with the field configuration.
func buildAssetObjectsQuery(objectTypeID, objectSchemaID, name, aql string) string {
	scope := fmt.Sprintf(`objectTypeId = %q`, objectTypeID)
	if aql != "" {
		scope = fmt.Sprintf(`(%s)`, aql)
	}
	if objectSchemaID != "" {
		scope = fmt.Sprintf(`%s AND objectSchemaId = %q`, scope, objectSchemaID)
	}
	return fmt.Sprintf(`%s AND name LIKE %q`, scope, name)
}

func parseObjectValuesOptions(c *gin.Context) (objectTypeID, objectSchemaID string, limit, offset int, err error) {
	objectTypeID = c.Query("object_type_id")
	if objectTypeID == "" {
		return "", "", 0, 0, fmt.Errorf("object_type_id query string is required")
	}
	objectSchemaID = c.Query("object_schema_id")
	limit, _ = strconv.Atoi(c.Query("limit"))
	if limit == 0 {
		return "", "", 0, 0, fmt.Errorf("limit query string is required and must not be 0")
	}
	offset, _ = strconv.Atoi(c.Query("offset"))
	return
}

// CreateIssueTemplates
//
//	@Summary		Create Issue Templates
//	@Description	Create Issue Templates
//	@Tags			Jira
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.JiraIssueTemplateRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.JiraIssueTemplate
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/issuetemplates [post]
func CreateIssueTemplates(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	if ctx.GetLicenseType() == license.OSSType {
		existing, err := models.ListJiraIssueTemplates(ctx.GetOrgID())
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing issue templates: %v", err)
			return
		}
		if len(existing) >= 1 {
			c.JSON(http.StatusForbidden, gin.H{"message": "Jira templates are limited to 1 in the Free plan"})
			return
		}
		if err := validateOssTemplateLimits(req); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
			return
		}
	}

	issue := &models.JiraIssueTemplate{
		ID:                              uuid.NewString(),
		OrgID:                           ctx.GetOrgID(),
		Name:                            req.Name,
		Description:                     req.Description,
		ProjectKey:                      req.ProjectKey,
		RequestTypeID:                   req.RequestTypeID,
		IssueTransitionNameOnClose:      req.IssueTransitionNameOnClose,
		SkipTransitionOnNonZeroExitCode: req.SkipTransitionOnNonZeroExitCode,
		MappingTypes:                    req.MappingTypes,
		PromptTypes:                     req.PromptTypes,
		CmdbTypes:                       req.CmdbTypes,
		ConnectionIDs:                   req.ConnectionIDs,
		CreatedAt:                       time.Now().UTC(),
		UpdatedAt:                       time.Now().UTC(),
	}
	err := models.CreateJiraIssueTemplates(issue)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusBadRequest, gin.H{"message": "jira integration is not enabled"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:                              issue.ID,
			Name:                            issue.Name,
			Description:                     issue.Description,
			ProjectKey:                      issue.ProjectKey,
			RequestTypeID:                   issue.RequestTypeID,
			IssueTransitionNameOnClose:      issue.IssueTransitionNameOnClose,
			SkipTransitionOnNonZeroExitCode: issue.SkipTransitionOnNonZeroExitCode,
			MappingTypes:                    issue.MappingTypes,
			PromptTypes:                     issue.PromptTypes,
			CmdbTypes:                       req.CmdbTypes,
			CreatedAt:                       issue.CreatedAt,
			UpdatedAt:                       issue.UpdatedAt,
			ConnectionIDs:                   issue.ConnectionIDs,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating issue templates: %v", err)
	}
}

// UpdateIssueTemplates
//
//	@Summary		Update Issue Templates
//	@Description	Update Issue Templates
//	@Tags			Jira
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string								true	"The id of the resource"
//	@Param			request		body		openapi.JiraIssueTemplateRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.JiraIssueTemplate
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/issuetemplates/{id} [put]
func UpdateIssueTemplates(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	if ctx.GetLicenseType() == license.OSSType {
		if err := validateOssTemplateLimits(req); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
			return
		}
	}

	issue := &models.JiraIssueTemplate{
		OrgID:                           ctx.GetOrgID(),
		ID:                              c.Param("id"),
		Name:                            req.Name,
		Description:                     req.Description,
		ProjectKey:                      req.ProjectKey,
		RequestTypeID:                   req.RequestTypeID,
		IssueTransitionNameOnClose:      req.IssueTransitionNameOnClose,
		SkipTransitionOnNonZeroExitCode: req.SkipTransitionOnNonZeroExitCode,
		MappingTypes:                    req.MappingTypes,
		PromptTypes:                     req.PromptTypes,
		CmdbTypes:                       req.CmdbTypes,
		ConnectionIDs:                   req.ConnectionIDs,
		UpdatedAt:                       time.Now().UTC(),
	}
	err := models.UpdateJiraIssueTemplates(issue)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:                              issue.ID,
			Name:                            issue.Name,
			Description:                     issue.Description,
			ProjectKey:                      issue.ProjectKey,
			RequestTypeID:                   issue.RequestTypeID,
			IssueTransitionNameOnClose:      issue.IssueTransitionNameOnClose,
			SkipTransitionOnNonZeroExitCode: issue.SkipTransitionOnNonZeroExitCode,
			MappingTypes:                    issue.MappingTypes,
			PromptTypes:                     issue.PromptTypes,
			CmdbTypes:                       issue.CmdbTypes,
			CreatedAt:                       issue.CreatedAt,
			UpdatedAt:                       issue.UpdatedAt,
			ConnectionIDs:                   issue.ConnectionIDs,
		})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating jira issue templates: %v", err)
	}
}

// DeleteIssueTemplates
//
//	@Summary		Delete Issue Templates
//	@Description	Delete Issue Templates
//	@Tags			Jira
//	@Produce		json
//	@Param			id	path	string	true	"The id of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira/issuetemplates/{id} [delete]
func DeleteIssueTemplates(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	templateID := c.Param("id")
	err := models.DeleteJiraIssueTemplates(ctx.GetOrgID(), templateID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed removing Jira issue templates: %v", err)
	}
}

func parseRequestPayload(c *gin.Context) *openapi.JiraIssueTemplateRequest {
	req := openapi.JiraIssueTemplateRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return nil
	}
	if req.IssueTransitionNameOnClose == "" {
		req.IssueTransitionNameOnClose = "done"
	}
	return &req
}
