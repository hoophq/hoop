package webhooks

import (
	"context"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2"
	svix "github.com/svix/svix-webhooks/go"
)

func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	appKey := os.Getenv("WEBHOOK_APPKEY")
	if appKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "webhook app key is not configured"})
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
	c.JSON(http.StatusOK, gin.H{"url": dashboard.Url})
}
