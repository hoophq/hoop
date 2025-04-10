package webhooks

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
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
func GetDashboardURL(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if webhookAppURL := appconfig.Get().WebhookAppURL(); webhookAppURL != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "dashboard is not available using self hosted Svix (webhook provider)"})
		return
	}

	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	dashboard, err := svixClient.Authentication.AppPortalAccess(
		context.Background(),
		ctx.GetOrgID(),
		&svix.AppPortalAccessIn{},
	)
	if isApiError(c, "portal-access", "", err) {
		return
	}
	c.JSON(http.StatusOK, openapi.WebhooksDashboardResponse{URL: dashboard.Url})
}

func getSvixClient() (*svix.Svix, error) {
	webhookAppKey := appconfig.Get().WebhookAppKey()
	if webhookAppKey == "" {
		return nil, fmt.Errorf("missing WEBHOOK_APPKEY environment variable")
	}
	webhookAppUrl := appconfig.Get().WebhookAppURL()
	return svix.New(webhookAppKey, &svix.SvixOptions{ServerUrl: webhookAppUrl}), nil
}
