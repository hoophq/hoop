package apifeatures

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const defaultReqTimeout = time.Second * 30

// OpenAI Chat Completions
//
//	@Summary		OpenAI Chat Completions
//	@Description	Proxy to OpenAI chat completions `/vi/chat/completions`
//	@Tags			Features
//	@Accepts		json
//	@Produces		json
//	@Success		200
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/features/ask-ai/v1/chat/completions [post]
func PostChatCompletions(c *gin.Context) {
	appConfig := appconfig.Get()
	if !appConfig.IsAskAIAvailable() {
		c.JSON(http.StatusBadRequest, gin.H{"message": "ask-api feature is unavailable"})
		return
	}
	ctx := storagev2.ParseContext(c)
	log := log.With("org", ctx.OrgName, "user", ctx.UserEmail)

	isFeatureEnabled, err := models.IsFeatureAskAiEnabled(ctx.OrgID)
	if err != nil {
		log.Errorf("unable to verify if ask-ai feature is enabled, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to verify feature status"})
		return
	}
	if !isFeatureEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "feature is disabled"})
		return
	}

	apiURL := fmt.Sprintf("%s/v1/chat/completions", appConfig.AskAIApiURL())
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), defaultReqTimeout)
	defer cancelFn()
	req, err := http.NewRequestWithContext(timeoutCtx, "POST", apiURL, c.Request.Body)
	if err != nil {
		log.Errorf("failed creating request to ask ai, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to create request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+appConfig.AskAIAPIKey())
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warnf("failed creating request to ask ai, reason=%#v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed performing request: %v", err)})
		return
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("failed reading resquest body, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed reading request body from remote api"})
		return
	}
	log.Debugf("writing response body for /v1/chat/completions, status=%v, response-length=%v",
		resp.StatusCode, len(data))
	if _, err := c.Writer.Write(data); err != nil {
		log.Errorf("failed writing response body, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed writing response body"})
		return
	}
	if resp.StatusCode > 299 {
		log.Warnf("remote api responded with non 2xx code, status=%v, headers=%v, response-body=%v, headers=%v", resp.Status, string(data), resp.Header)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
}
