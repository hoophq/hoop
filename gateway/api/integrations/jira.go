package apijiraintegration

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	jiraintegration "github.com/hoophq/hoop/gateway/pgrest/jiraintegration"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// GetJiraIntegration
//
//	@Summary		Get Jira Integration
//	@Description	Get Jira integration for the organization
//	@Tags			Jira
//	@Produce		json
//	@Success		200	{object}	openapi.JiraIntegration
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/jira-integration [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	integration, err := jiraintegration.NewJiraIntegrations().GetJiraIntegration(ctx.OrgID)
	if err != nil {
		log.Errorf("failed fetching Jira integration, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if integration == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "Jira integration not found"})
		return
	}
	c.JSON(http.StatusOK, openapi.JiraIntegration{
		ID:             integration.ID,
		OrgID:          integration.OrgID,
		JiraURL:        integration.JiraURL,
		JiraUser:       integration.JiraUser,
		JiraProjectKey: integration.JiraProjectKey,
		JiraAPIToken:   integration.JiraAPIToken,
		Status:         openapi.JiraIntegrationStatus(integration.Status),
		CreatedAt:      integration.CreatedAt,
		UpdatedAt:      integration.UpdatedAt,
	})
}

// CreateJiraIntegration
//
//	@Summary		Create Jira Integration
//	@Description	Create a new Jira integration for the organization
//	@Tags			Jira
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.JiraIntegration	true	"The request body resource"
//	@Success		201		{object}	openapi.JiraIntegration
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/jira-integration [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.JiraIntegration
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingIntegration, err := jiraintegration.NewJiraIntegrations().GetJiraIntegration(ctx.OrgID)
	if err != nil {
		log.Errorf("failed checking existing Jira integration, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if existingIntegration != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "Jira integration already exists for this organization"})
		return
	}

	newIntegration := jiraintegration.JiraIntegration{
		OrgID:          ctx.GetOrgID(),
		JiraURL:        req.JiraURL,
		JiraUser:       req.JiraUser,
		JiraAPIToken:   req.JiraAPIToken,
		JiraProjectKey: req.JiraProjectKey,
		Status:         jiraintegration.JiraIntegrationStatus(req.Status),
	}

	createdIntegration, err := jiraintegration.NewJiraIntegrations().CreateJiraIntegration(ctx.OrgID, newIntegration)
	if err != nil {
		log.Errorf("failed creating Jira integration, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
//	@Param			request	body		openapi.JiraIntegration	true	"The request body resource"
//	@Success		200		{object}	openapi.JiraIntegration
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/jira-integration [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.JiraIntegration
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingIntegration, err := jiraintegration.NewJiraIntegrations().GetJiraIntegration(ctx.OrgID)
	if err != nil {
		log.Errorf("failed fetching existing Jira integration, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if existingIntegration == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "Jira integration not found"})
		return
	}

	updatedIntegration, err := jiraintegration.NewJiraIntegrations().UpdateJiraIntegration(ctx.OrgID, jiraintegration.JiraIntegration{
		JiraURL:        req.JiraURL,
		JiraUser:       req.JiraUser,
		JiraAPIToken:   req.JiraAPIToken,
		JiraProjectKey: req.JiraProjectKey,
		Status:         jiraintegration.JiraIntegrationStatus(req.Status),
	})
	if err != nil {
		log.Errorf("failed updating Jira integration, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, toOpenAPIJiraIntegration(updatedIntegration))
}

// Helper function to convert jiraintegration.JiraIntegration to openapi.JiraIntegration
func toOpenAPIJiraIntegration(integration *jiraintegration.JiraIntegration) openapi.JiraIntegration {
	return openapi.JiraIntegration{
		ID:             integration.ID,
		OrgID:          integration.OrgID,
		JiraURL:        integration.JiraURL,
		JiraUser:       integration.JiraUser,
		JiraProjectKey: integration.JiraProjectKey,
		JiraAPIToken:   integration.JiraAPIToken,
		Status:         openapi.JiraIntegrationStatus(integration.Status),
		CreatedAt:      integration.CreatedAt,
		UpdatedAt:      integration.UpdatedAt,
	}
}
