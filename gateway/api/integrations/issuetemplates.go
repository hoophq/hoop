package apijiraintegration

import (
	"fmt"
	"net/http"
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
			ID:            issue.ID,
			Name:          issue.Name,
			Description:   issue.Description,
			ProjectKey:    issue.ProjectKey,
			RequestTypeID: issue.RequestTypeID,
			MappingTypes:  issue.MappingTypes,
			PromptTypes:   issue.PromptTypes,
			CmdbTypes:     issue.CmdbTypes,
			CreatedAt:     issue.CreatedAt,
			UpdatedAt:     issue.UpdatedAt,
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
	issue, config, err := models.GetJiraIssueTemplatesByID(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		cmdbTypes, err := cmdbTypesWithExternalObjects(c, config, issue.CmdbTypes)
		if err != nil {
			log.Errorf("failed listing objects from Jira assets api, reason=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing objects from Jira assets api"})
			return
		}
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:            issue.ID,
			Name:          issue.Name,
			Description:   issue.Description,
			ProjectKey:    issue.ProjectKey,
			RequestTypeID: issue.RequestTypeID,
			MappingTypes:  issue.MappingTypes,
			PromptTypes:   issue.PromptTypes,
			CmdbTypes:     cmdbTypes,
			CreatedAt:     issue.CreatedAt,
			UpdatedAt:     issue.UpdatedAt,
		})
	default:
		log.Errorf("failed listing issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
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
		ID:            uuid.NewString(),
		OrgID:         ctx.GetOrgID(),
		Name:          req.Name,
		Description:   req.Description,
		ProjectKey:    req.ProjectKey,
		RequestTypeID: req.RequestTypeID,
		MappingTypes:  req.MappingTypes,
		PromptTypes:   req.PromptTypes,
		CmdbTypes:     req.CmdbTypes,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	err := models.CreateJiraIssueTemplates(issue)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusBadRequest, gin.H{"message": "jira integration is not enabled"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:            issue.ID,
			Name:          issue.Name,
			Description:   issue.Description,
			ProjectKey:    issue.ProjectKey,
			RequestTypeID: issue.RequestTypeID,
			MappingTypes:  issue.MappingTypes,
			PromptTypes:   issue.PromptTypes,
			CmdbTypes:     req.CmdbTypes,
			CreatedAt:     issue.CreatedAt,
			UpdatedAt:     issue.UpdatedAt,
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
		OrgID:         ctx.GetOrgID(),
		ID:            c.Param("id"),
		Name:          req.Name,
		Description:   req.Description,
		ProjectKey:    req.ProjectKey,
		RequestTypeID: req.RequestTypeID,
		MappingTypes:  req.MappingTypes,
		PromptTypes:   req.PromptTypes,
		CmdbTypes:     req.CmdbTypes,
		UpdatedAt:     time.Now().UTC(),
	}
	err := models.UpdateJiraIssueTemplates(issue)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	case nil:
		c.JSON(http.StatusOK, &openapi.JiraIssueTemplate{
			ID:            issue.ID,
			Name:          issue.Name,
			Description:   issue.Description,
			ProjectKey:    issue.ProjectKey,
			RequestTypeID: issue.RequestTypeID,
			MappingTypes:  issue.MappingTypes,
			PromptTypes:   issue.PromptTypes,
			CmdbTypes:     issue.CmdbTypes,
			CreatedAt:     issue.CreatedAt,
			UpdatedAt:     issue.UpdatedAt,
		})
	default:
		log.Errorf("failed updating jira issue templates, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

}

// DeleteConnection
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
	err := models.DeleteJiraIssueTemplates(ctx.GetOrgID(), c.Param("id"))
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
	return &req
}

func cmdbTypesWithExternalObjects(ctx *gin.Context, config *models.JiraIntegration, cmdbTypes map[string]any) (map[string]any, error) {
	if len(cmdbTypes) == 0 || config == nil || !config.IsActive() || ctx.Query("expand") != "cmdbtype-values" {
		return cmdbTypes, nil
	}

	itemList, ok := cmdbTypes["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("unable to decode cmdb items attribute, type=%T", cmdbTypes["items"])
	}
	// fmt.Printf("AQL QUERY RESPONSE: %#v\n", response)
	for i, obj := range itemList {
		item, ok := obj.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unable to decode cmdb item, type=%T", item)
		}
		if item["jira_object_type"] == nil {
			return nil, fmt.Errorf("jira_object_type is missing, record=%v, item=%#v", i, item)
		}
		objectType := fmt.Sprintf("%v", item["jira_object_type"])
		responseItems, err := jira.FetchObjectsByAQL(config, `objectType = %q`, objectType)
		if err != nil {
			return nil, fmt.Errorf("record=%v, %v", i, err)
		}
		log.Infof("jira assets api response, object-type=%v, total-requests=%v",
			objectType, len(responseItems))

		jiraValues := []map[string]any{}
		for _, response := range responseItems {
			for _, val := range response.Values {
				jiraValues = append(jiraValues, map[string]any{"id": val.GlobalID, "name": val.Name})
			}
		}
		item["jira_values"] = jiraValues
	}
	return cmdbTypes, nil
}
