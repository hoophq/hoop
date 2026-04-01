package apijiraintegration

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// GetJiraIntegration
//
//	@Summary		Get Jira Integration
//	@Description	Get Jira integration for the organization
//	@Tags			Jira
//	@Produce		json
//	@Success		200		{object}	openapi.JiraIntegration
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	dbJiraIntegration, err := models.GetJiraIntegration(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching Jira integration")
		return
	}
	if dbJiraIntegration == nil {
		c.JSON(http.StatusOK, nil)
		return
	}

	// Created as a temporary solution so that non-admin users can check if the integration is active
	// TODO: Implement a more robust solution so that non-admin users can check if the integration is active
	if !ctx.IsAdmin() {
		dbJiraIntegration.APIToken = ""
	}

	c.JSON(http.StatusOK, openapi.JiraIntegration{
		ID:        dbJiraIntegration.ID,
		OrgID:     dbJiraIntegration.OrgID,
		URL:       dbJiraIntegration.URL,
		User:      dbJiraIntegration.User,
		APIToken:  dbJiraIntegration.APIToken,
		Status:    openapi.JiraIntegrationStatus(dbJiraIntegration.Status),
		CreatedAt: dbJiraIntegration.CreatedAt,
		UpdatedAt: dbJiraIntegration.UpdatedAt,
	})
}

// CreateJiraIntegration
//
//	@Summary		Create Jira Integration
//	@Description	Create a new Jira integration for the organization
//	@Tags			Jira
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.JiraIntegration	true	"The request body resource"
//	@Success		201			{object}	openapi.JiraIntegration
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.JiraIntegration
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	dbExistingJiraIntegration, err := models.GetJiraIntegration(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed checking existing Jira integration")
		return
	}
	if dbExistingJiraIntegration != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "Jira integration already exists for this organization"})
		return
	}

	newIntegration := models.JiraIntegration{
		ID:       uuid.NewString(),
		OrgID:    ctx.GetOrgID(),
		URL:      req.URL,
		User:     req.User,
		APIToken: req.APIToken,
		Status:   models.JiraIntegrationStatus(req.Status),
	}

	createdIntegration, err := models.CreateJiraIntegration(ctx.OrgID, &newIntegration)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating Jira integration")
		return
	}

	c.JSON(http.StatusCreated, toOpenAPIJiraIntegration(createdIntegration))
}

// UpdateJiraIntegration
//
//	@Summary		Update Jira Integration
//	@Description	Update the Jira integration for the organization
//	@Tags			Jira
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.JiraIntegration	true	"The request body resource"
//	@Success		200			{object}	openapi.JiraIntegration
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/integrations/jira [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.JiraIntegration
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingIntegration, err := models.GetJiraIntegration(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching existing Jira integration")
		return
	}
	if existingIntegration == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "Jira integration not found"})
		return
	}

	updatedIntegration, err := models.UpdateJiraIntegration(ctx.OrgID, &models.JiraIntegration{
		URL:      req.URL,
		User:     req.User,
		APIToken: req.APIToken,
		Status:   models.JiraIntegrationStatus(req.Status),
	})
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating Jira integration")
		return
	}

	c.JSON(http.StatusOK, toOpenAPIJiraIntegration(updatedIntegration))
}

// Helper function to convert jiraintegration.JiraIntegration to openapi.JiraIntegration
func toOpenAPIJiraIntegration(integration *models.JiraIntegration) openapi.JiraIntegration {
	return openapi.JiraIntegration{
		ID:        integration.ID,
		OrgID:     integration.OrgID,
		URL:       integration.URL,
		User:      integration.User,
		APIToken:  integration.APIToken,
		Status:    openapi.JiraIntegrationStatus(integration.Status),
		CreatedAt: integration.CreatedAt,
		UpdatedAt: integration.UpdatedAt,
	}
}
