package featureflags

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/transport"
)

// List
//
//	@Summary		List Feature Flags
//	@Description	List all feature flags from the catalog with their per-org state
//	@Tags			Feature Flags
//	@Produce		json
//	@Success		200			{array}		openapi.FeatureFlagItem
//	@Failure		500			{object}	openapi.HTTPError
//	@Router			/feature-flags [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID := ctx.GetOrgID()

	dbFlags, err := models.ListOrgFeatureFlags(orgID)
	if err != nil {
		log.Errorf("failed listing feature flags: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing feature flags"})
		return
	}

	dbMap := make(map[string]bool, len(dbFlags))
	for _, f := range dbFlags {
		dbMap[f.Name] = f.Enabled
	}

	catalogFlags := featureflag.All()
	items := make([]openapi.FeatureFlagItem, 0, len(catalogFlags))
	for _, f := range catalogFlags {
		enabled := f.Default
		if val, ok := dbMap[f.Name]; ok {
			enabled = val
		}
		components := make([]string, len(f.Components))
		for i, comp := range f.Components {
			components[i] = string(comp)
		}
		items = append(items, openapi.FeatureFlagItem{
			Name:        f.Name,
			Description: f.Description,
			Default:     f.Default,
			Stability:   string(f.Stability),
			Components:  components,
			Enabled:     enabled,
		})
	}

	c.JSON(http.StatusOK, items)
}

// Update
//
//	@Summary		Update Feature Flag
//	@Description	Enable or disable a feature flag for the organization
//	@Tags			Feature Flags
//	@Param			name	path	string							true	"Feature flag name"
//	@Param			request	body	openapi.FeatureFlagUpdateRequest	true	"The request body"
//	@Accept			json
//	@Produce		json
//	@Success		200			{object}	openapi.FeatureFlagItem
//	@Failure		400,403,500	{object}	openapi.HTTPError
//	@Router			/feature-flags/{name} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID := ctx.GetOrgID()
	flagName := c.Param("name")

	f, ok := featureflag.Lookup(flagName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("unknown feature flag: %s", flagName)})
		return
	}

	var req openapi.FeatureFlagUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	updatedBy := ctx.UserEmail
	if err := models.UpsertOrgFeatureFlag(orgID, flagName, req.Enabled, updatedBy); err != nil {
		log.Errorf("failed upserting feature flag: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating feature flag"})
		return
	}

	featureflag.Set(orgID, flagName, req.Enabled)

	go transport.SendFeatureFlagUpdateToOrg(orgID)

	components := make([]string, len(f.Components))
	for i, comp := range f.Components {
		components[i] = string(comp)
	}
	c.JSON(http.StatusOK, openapi.FeatureFlagItem{
		Name:        f.Name,
		Description: f.Description,
		Default:     f.Default,
		Stability:   string(f.Stability),
		Components:  components,
		Enabled:     req.Enabled,
	})
}
