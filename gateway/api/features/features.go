package apifeatures

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func isValid(r openapi.FeatureRequest, c *gin.Context) (valid bool) {
	if r.Status != openapi.FeatureStatusEnabled && r.Status != openapi.FeatureStatusDisabled {
		msgErr := fmt.Sprintf("status attribute invalid, accept only: [%s, %s]",
			openapi.FeatureStatusEnabled, openapi.FeatureStatusDisabled)
		c.JSON(http.StatusBadRequest, gin.H{"message": msgErr})
		return
	}
	if !slices.Contains(openapi.FeatureList, r.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("feature %s is not implemented", r.Name)})
		return
	}
	if !appconfig.Get().IsAskAIAvailable() {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("feature %s is not configured", r.Name)})
		return
	}
	return true
}

// FeatureUpdate
//
//	@Summary		Feature Update
//	@Description	Updates a feature configuration. It will report if this feature is available in the user info endpoint.
//	@Tags			Features
//	@Produces		json
//	@Param			request	body	openapi.FeatureRequest	true	"The request body resource"
//	@Success		204
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/orgs/features [put]
func FeatureUpdate(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.FeatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if !isValid(req, c) {
		return
	}

	metadata := map[string]any{
		"app_version": version.Get().Version,
		"user_agent":  c.Request.Header.Get("user-agent"),
	}

	eventName := models.FeatureAskAiDisabled
	if req.Status == openapi.FeatureStatusEnabled {
		eventName = models.FeatureAskAiEnabled
	}
	err := models.CreateAudit(ctx.OrgID, eventName, ctx.UserEmail, metadata)
	if err != nil {
		log.Errorf("fail to update feature %v, reason=%v", req.Name, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to update feature"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
