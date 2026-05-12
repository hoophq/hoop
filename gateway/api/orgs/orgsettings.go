package apiorgs

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// GetOrgAnalyticsMode
//
//	@Summary		Get Organization Analytics Mode
//	@Description	Get the analytics privacy mode of the caller's organization
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.OrgAnalyticsModeResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/analytics-mode [get]
func GetOrgAnalyticsMode(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load organization: %v", err)
		return
	}
	mode := org.AnalyticsMode
	if !models.IsValidAnalyticsMode(mode) {
		mode = models.AnalyticsModeAnonymous
	}
	c.JSON(http.StatusOK, openapi.OrgAnalyticsModeResponse{
		AnalyticsMode: openapi.AnalyticsModeType(mode),
	})
}

// UpdateOrgAnalyticsMode
//
//	@Summary		Update Organization Analytics Mode
//	@Description	Update the analytics privacy mode of the caller's organization
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.OrgAnalyticsModeRequest	true	"The new analytics mode"
//	@Success		200		{object}	openapi.OrgAnalyticsModeResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/orgs/analytics-mode [put]
func UpdateOrgAnalyticsMode(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.OrgAnalyticsModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	mode := string(req.AnalyticsMode)
	if !models.IsValidAnalyticsMode(mode) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid analytics_mode, accepted values are 'identified', 'anonymous', 'disabled'"})
		return
	}
	if err := models.UpdateOrgAnalyticsMode(ctx.OrgID, mode); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update analytics mode: %v", err)
		return
	}
	analytics.SetMode(ctx.OrgID, mode)
	c.JSON(http.StatusOK, openapi.OrgAnalyticsModeResponse{
		AnalyticsMode: openapi.AnalyticsModeType(mode),
	})
}
