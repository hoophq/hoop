package apiorgs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
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
	previousMode := analytics.GetMode(ctx.OrgID)
	if err := models.UpdateOrgAnalyticsMode(ctx.OrgID, mode); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update analytics mode: %v", err)
		return
	}

	// Emit BEFORE updating the cache so Track's internal disabled-mode check
	// reads the previous mode — captures opt-out transitions and respects an
	// already-disabled org for opt-in transitions.
	if previousMode != mode {
		trackClient := analytics.New()
		defer trackClient.Close()
		trackClient.Track(ctx.UserID, analytics.EventUpdateOrgAnalyticsMode, map[string]any{
			"org-id":                  ctx.OrgID,
			"new-analytics-mode":      mode,
			"previous-analytics-mode": previousMode,
		})
	}

	analytics.SetMode(ctx.OrgID, mode)
	c.JSON(http.StatusOK, openapi.OrgAnalyticsModeResponse{
		AnalyticsMode: openapi.AnalyticsModeType(mode),
	})
}

// GetOrgHideRoleInfo
//
//	@Summary		Get Organization Hide Role Info
//	@Description	Get whether the caller's organization blocks reading connection/role secrets (envvars) through the API
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.OrgHideRoleInfoResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/hide-role-info [get]
func GetOrgHideRoleInfo(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load organization: %v", err)
		return
	}
	c.JSON(http.StatusOK, openapi.OrgHideRoleInfoResponse{
		HideRoleInfo: org.HideRoleInfo,
	})
}

// UpdateOrgHideRoleInfo
//
//	@Summary		Update Organization Hide Role Info
//	@Description	Toggle whether the caller's organization blocks reading connection/role secrets (envvars) through the API
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.OrgHideRoleInfoRequest	true	"The new hide role info setting"
//	@Success		200		{object}	openapi.OrgHideRoleInfoResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/orgs/hide-role-info [put]
func UpdateOrgHideRoleInfo(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.OrgHideRoleInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := models.UpdateOrgHideRoleInfo(models.DB, ctx.OrgID, *req.HideRoleInfo); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update hide role info: %v", err)
		return
	}
	c.JSON(http.StatusOK, openapi.OrgHideRoleInfoResponse{
		HideRoleInfo: *req.HideRoleInfo,
	})
}

// GetOrgProtectionProfile
//
//	@Summary		Get Organization Protection Profile
//	@Description	Get the organization's default protection profile. A null profile means manual configuration.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.OrgProtectionProfileResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/protection-profile [get]
func GetOrgProtectionProfile(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load organization: %v", err)
		return
	}
	c.JSON(http.StatusOK, openapi.OrgProtectionProfileResponse{
		Profile:       org.DefaultProtectionProfile,
		AttributeName: services.ProtectionProfileAttributeName(org.DefaultProtectionProfile),
	})
}

// UpdateOrgProtectionProfile
//
//	@Summary		Update Organization Protection Profile
//	@Description	Select the organization's default protection profile. The backend materializes the profile's managed rules and attribute and tags every connection. Passing a null profile switches to manual configuration and deletes all Hoop-managed protection rules.
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.OrgProtectionProfileRequest	true	"The protection profile selection"
//	@Success		200		{object}	openapi.OrgProtectionProfileResponse
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/orgs/protection-profile [put]
func UpdateOrgProtectionProfile(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.OrgProtectionProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Source != "onboarding" && req.Source != "settings" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid source, accepted values are 'onboarding', 'settings'"})
		return
	}
	if req.Profile != nil &&
		services.IsEnterpriseProtectionProfile(*req.Profile) &&
		getLicenseType(ctx) != license.EnterpriseType {
		c.JSON(http.StatusForbidden, gin.H{"message": "this protection profile requires an enterprise license, contact our support at https://help.hoop.dev"})
		return
	}
	orgID, err := uuid.Parse(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid organization id: %v", err)
		return
	}
	result, err := services.ApplyOrgProtectionProfile(c.Request.Context(), orgID, req.Profile, "")
	switch {
	case errors.Is(err, services.ErrInvalidProtectionProfile):
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid profile, accepted values are 'hipaa-ready', 'soc2-type2', 'protection-permissive', 'protection-medium', 'protection-high' or null"})
		return
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to apply protection profile: %v", err)
		return
	}

	selected := "manual"
	if req.Profile != nil {
		selected = *req.Profile
	}
	previous := "manual"
	if result.PreviousProfile != nil {
		previous = *result.PreviousProfile
	}
	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.Track(ctx.UserID, analytics.EventProtectionProfileSelected, map[string]any{
		"org-id":               ctx.OrgID,
		"selected-profile":     selected,
		"previous-profile":     previous,
		"source":               req.Source,
		"connections-affected": result.ConnectionsAffected,
	})

	c.JSON(http.StatusOK, openapi.OrgProtectionProfileResponse{
		Profile:       req.Profile,
		AttributeName: services.ProtectionProfileAttributeName(req.Profile),
	})
}

func getLicenseType(ctx *storagev2.Context) string {
	licenseType := license.OSSType
	if ctx.OrgLicenseData != nil && len(*ctx.OrgLicenseData) > 0 {
		var l license.License
		if err := json.Unmarshal(*ctx.OrgLicenseData, &l); err == nil {
			licenseType = l.Payload.Type
		}
	}
	return licenseType
}
