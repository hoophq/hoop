package apijiraintegration

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

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
		log.Errorf("failed listing issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	issues := []openapi.JiraIssueTemplate{}
	for _, issue := range issueList {
		issues = append(issues, openapi.JiraIssueTemplate{
			ID:                         issue.ID,
			Name:                       issue.Name,
			Description:                issue.Description,
			ProjectKey:                 issue.ProjectKey,
			RequestTypeID:              issue.RequestTypeID,
			IssueTransitionNameOnClose: issue.IssueTransitionNameOnClose,
			MappingTypes:               issue.MappingTypes,
			PromptTypes:                issue.PromptTypes,
			CmdbTypes:                  issue.CmdbTypes,
			ConnectionIDs:              issue.ConnectionIDs,
			CreatedAt:                  issue.CreatedAt,
			UpdatedAt:                  issue.UpdatedAt,
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
			ID:                         issue.ID,
			Name:                       issue.Name,
			Description:                issue.Description,
			ProjectKey:                 issue.ProjectKey,
			RequestTypeID:              issue.RequestTypeID,
			IssueTransitionNameOnClose: issue.IssueTransitionNameOnClose,
			MappingTypes:               issue.MappingTypes,
			PromptTypes:                issue.PromptTypes,
			CmdbTypes:                  issue.CmdbTypes,
			ConnectionIDs:              issue.ConnectionIDs,
			CreatedAt:                  issue.CreatedAt,
			UpdatedAt:                  issue.UpdatedAt,
		})
	default:
		log.Errorf("failed listing issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
//	@Success		200					{object} openapi.JiraAssetObjects
//	@Failure		400,404,500			{object}	openapi.HTTPError
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
		errMsg := fmt.Sprintf("failed obtaining jira integration configuration, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	query := fmt.Sprintf(`objectTypeId = %q AND name LIKE %q`, objectTypeID, c.Query("name"))
	if objectSchemaID != "" {
		query = fmt.Sprintf(`objectTypeId = %q AND objectSchemaId = %q AND name LIKE %q`,
			objectTypeID, objectSchemaID, c.Query("name"))
	}

	resp, err := jira.FetchObjectsByAQL(config, limit, offset, query)
	if err != nil {
		log.Errorf("failed fetching object type values from Jira, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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

	issue := &models.JiraIssueTemplate{
		ID:                         uuid.NewString(),
		OrgID:                      ctx.GetOrgID(),
		Name:                       req.Name,
		Description:                req.Description,
		ProjectKey:                 req.ProjectKey,
		RequestTypeID:              req.RequestTypeID,
		IssueTransitionNameOnClose: req.IssueTransitionNameOnClose,
		MappingTypes:               req.MappingTypes,
		PromptTypes:                req.PromptTypes,
		CmdbTypes:                  req.CmdbTypes,
		ConnectionIDs:              req.ConnectionIDs,
		CreatedAt:                  time.Now().UTC(),
		UpdatedAt:                  time.Now().UTC(),
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
			ID:                         issue.ID,
			Name:                       issue.Name,
			Description:                issue.Description,
			ProjectKey:                 issue.ProjectKey,
			RequestTypeID:              issue.RequestTypeID,
			IssueTransitionNameOnClose: issue.IssueTransitionNameOnClose,
			MappingTypes:               issue.MappingTypes,
			PromptTypes:                issue.PromptTypes,
			CmdbTypes:                  req.CmdbTypes,
			CreatedAt:                  issue.CreatedAt,
			UpdatedAt:                  issue.UpdatedAt,
			ConnectionIDs:              issue.ConnectionIDs,
		})
	default:
		log.Errorf("failed creting issue templates, reason=%v, err=%T", err, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
	issue := &models.JiraIssueTemplate{
		OrgID:                      ctx.GetOrgID(),
		ID:                         c.Param("id"),
		Name:                       req.Name,
		Description:                req.Description,
		ProjectKey:                 req.ProjectKey,
		RequestTypeID:              req.RequestTypeID,
		IssueTransitionNameOnClose: req.IssueTransitionNameOnClose,
		MappingTypes:               req.MappingTypes,
		PromptTypes:                req.PromptTypes,
		CmdbTypes:                  req.CmdbTypes,
		ConnectionIDs:              req.ConnectionIDs,
		UpdatedAt:                  time.Now().UTC(),
	}
	err := models.UpdateJiraIssueTemplates(issue)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:                         issue.ID,
			Name:                       issue.Name,
			Description:                issue.Description,
			ProjectKey:                 issue.ProjectKey,
			RequestTypeID:              issue.RequestTypeID,
			IssueTransitionNameOnClose: issue.IssueTransitionNameOnClose,
			MappingTypes:               issue.MappingTypes,
			PromptTypes:                issue.PromptTypes,
			CmdbTypes:                  issue.CmdbTypes,
			CreatedAt:                  issue.CreatedAt,
			UpdatedAt:                  issue.UpdatedAt,
			ConnectionIDs:              issue.ConnectionIDs,
		})
	default:
		log.Errorf("failed updating jira issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
		log.Errorf("failed removing Jira issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
