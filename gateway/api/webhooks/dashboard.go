package webhooks

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2"
	svix "github.com/svix/svix-webhooks/go"
)

// OpenWebhookDashboard
//
//	@Summary		Get Webhooks Dashboard
//	@Description	Get webhooks dashboard url
//	@Tags			Features
//	@Produce		json
//	@Success		200	{object}	openapi.WebhooksDashboardResponse
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/webhooks-dashboard [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	appKey := appconfig.Get().WebhookAppKey()
	if appKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "webhook app key is not configured"})
		return
	}
	if webhookAppURL := appconfig.Get().WebhookAppURL(); webhookAppURL != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "dashboard is not available using self hosted Svix (webhook provider)"})
		return
	}
	svixClient := svix.New(appKey, nil)
	dashboard, err := svixClient.Authentication.AppPortalAccess(
		context.Background(),
		ctx.GetOrgID(),
		&svix.AppPortalAccessIn{},
	)
	if err != nil {
		log.Errorf("failed obtaining dashboard url from svix, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": "failed obtaining the dashboard url"})
		return
	}
	c.JSON(http.StatusOK, openapi.WebhooksDashboardResponse{URL: dashboard.Url})
}
