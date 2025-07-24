package apiserverconfig

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
)

const defaultGrpcServerURL = "grpc://127.0.0.1:8010"

// GetServerMiscellaneous
//
//	@Summary		Get Server Miscellaneous Configuration
//	@Description	Get server miscellaneous configuration
//	@Tags			Server Management
//	@Produce		json
//	@Success		200			{object}	openapi.ServerMiscConfig
//	@Failure		403,404,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/misc [get]
func GetServerMisc(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}

	config, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		errMsg := fmt.Sprintf("failed to get server config, reason=%v", err)
		log.Errorf(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	if config == nil {
		config = &models.ServerMiscConfig{}
	}

	if config == nil {
		config = &models.ServerMiscConfig{}
	}

	appc := appconfig.Get()
	productAnalytics := "active"
	if !appc.AnalyticsTracking() {
		productAnalytics = "inactive"
	}

	grpcURL := appc.GrpcURL()
	if config.ProductAnalytics != nil {
		productAnalytics = *config.ProductAnalytics
	}
	if config.GrpcServerURL != nil {
		grpcURL = *config.GrpcServerURL
	}

	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics: productAnalytics,
		GrpcServerURL:    grpcURL,
	})
}

// UpdateServerMisc
//
//	@Summary		Update Server Miscellaneous Configuration
//	@Description	Update server miscellaneous configuration
//	@Tags			Server Management
//	@Param			request	body	openapi.ServerMiscConfig	true	"The request body resource"
//	@Accept			json
//	@Produce		json
//	@Success		200			{object}	openapi.ServerMiscConfig
//	@Failure		400,403,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/misc [put]
func UpdateServerMisc(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}

	req, err := parseMiscPayload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	updatedConfig, err := models.UpsertServerMiscConfig(&models.ServerMiscConfig{
		ProductAnalytics: req.ProductAnalytics,
		GrpcServerURL:    req.GrpcServerURL,
	})
	if err != nil {
		log.Errorf("failed to update server config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update server config"})
		return
	}

	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics: ptr.ToString(updatedConfig.ProductAnalytics),
		GrpcServerURL:    ptr.ToString(updatedConfig.GrpcServerURL),
	})
}

func parseMiscPayload(c *gin.Context) (*models.ServerMiscConfig, error) {
	var req openapi.ServerMiscConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed to bind request, reason=%v", err)
		return nil, err
	}

	invalidStatus := req.ProductAnalytics != "active" && req.ProductAnalytics != "inactive"
	if invalidStatus {
		return nil, fmt.Errorf("invalid attribute for 'product_analytics', accepted values are 'active' or 'inactive'")
	}

	if req.GrpcServerURL == "" {
		req.GrpcServerURL = defaultGrpcServerURL
	}

	validPrefixes := []string{"grpc://", "grpcs://", "http://", "https://"}
	hasValidPrefix := false
	for _, prefix := range validPrefixes {
		if strings.HasPrefix(req.GrpcServerURL, prefix) {
			hasValidPrefix = true
			break
		}
	}

	if !hasValidPrefix {
		return nil, fmt.Errorf("invalid attribute for 'grpc_server_url', it must start with 'grpc://' or 'grpcs://'")
	}

	return &models.ServerMiscConfig{
		ProductAnalytics: &req.ProductAnalytics,
		GrpcServerURL:    &req.GrpcServerURL,
	}, nil
}

func forbiddenOnMultiTenantSetups(c *gin.Context) (forbidden bool) {
	if appconfig.Get().OrgMultitenant() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "this operation is not allowed in multi-tenant mode"})
		return true
	}
	return false
}
