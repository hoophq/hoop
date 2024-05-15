package apiorgs

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/appconfig"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgaudit "github.com/runopsio/hoop/gateway/pgrest/audit"
	"github.com/runopsio/hoop/gateway/storagev2"
)

var (
	featureList = []string{"ask-ai"}
)

const (
	FeatureStatusEnabled  string = "enabled"
	FeatureStatusDisabled string = "disabled"
)

type FeatureRequest struct {
	Name   string `json:"name" binding:"required"`
	Status string `json:"status" binding:"required"`
}

func (r FeatureRequest) isValid(c *gin.Context) (valid bool) {
	if r.Status != FeatureStatusEnabled && r.Status != FeatureStatusDisabled {
		msgErr := fmt.Sprintf("status attribute invalid, accept only: [%s, %s]",
			FeatureStatusEnabled, FeatureStatusDisabled)
		c.JSON(http.StatusBadRequest, gin.H{"message": msgErr})
		return
	}
	if !slices.Contains(featureList, r.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("feature %s is not implemented", r.Name)})
		return
	}
	if !appconfig.Get().IsAskAIAvailable() {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("feature %s is not configured", r.Name)})
		return
	}
	return true
}

func FeatureUpdate(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req FeatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if !req.isValid(c) {
		return
	}

	metadata := map[string]any{
		"app_version": version.Get().Version,
		"user_agent":  c.Request.Header.Get("user-agent"),
	}

	eventName := pgaudit.FeatureAskAiDisabled
	if req.Status == FeatureStatusEnabled {
		eventName = pgaudit.FeatureAskAiEnabled
	}
	auditCtx := pgrest.NewAuditContext(ctx.GetOrgID(), eventName, ctx.UserEmail).WithMetadata(metadata)
	if err := pgaudit.New().Create(auditCtx); err != nil {
		log.Errorf("fail to update feature %v, reason=%v", req.Name, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to update feature"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
