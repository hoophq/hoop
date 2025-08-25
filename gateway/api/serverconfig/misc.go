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
	"github.com/hoophq/hoop/gateway/proxyproto"
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

	var pgServerConfig *openapi.PostgresServerConfig
	if config.PostgresServerConfig != nil {
		pgServerConfig = &openapi.PostgresServerConfig{
			ListenAddress: config.PostgresServerConfig.ListenAddress,
		}
	}

	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics:     productAnalytics,
		GrpcServerURL:        grpcURL,
		PostgresServerConfig: pgServerConfig,
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

	pgProxyServer := proxyproto.GetPostgresServerInstance()
	enablePgServerProxy := req.PostgresServerConfig != nil
	if enablePgServerProxy {
		currentSrvConfig, err := models.GetServerMiscConfig()
		if err != nil && err != models.ErrNotFound {
			log.Errorf("failed to get server config, reason=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}

		// Stop the server if the listen address has changed
		if currentSrvConfig != nil && currentSrvConfig.PostgresServerConfig != nil {
			if currentSrvConfig.PostgresServerConfig.ListenAddress != req.PostgresServerConfig.ListenAddress {
				_ = pgProxyServer.Stop()
			}
		}

		if err := pgProxyServer.Start(req.PostgresServerConfig.ListenAddress); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
	} else {
		if err := pgProxyServer.Stop(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
	}

	updatedConfig, err := models.UpsertServerMiscConfig(&models.ServerMiscConfig{
		ProductAnalytics:     req.ProductAnalytics,
		GrpcServerURL:        req.GrpcServerURL,
		PostgresServerConfig: req.PostgresServerConfig,
	})
	if err != nil {
		log.Errorf("failed to update server config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update server config"})
		return
	}

	var pgServerConfig *openapi.PostgresServerConfig
	if updatedConfig.PostgresServerConfig != nil {
		pgServerConfig = &openapi.PostgresServerConfig{
			ListenAddress: updatedConfig.PostgresServerConfig.ListenAddress,
		}
	}

	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics:     ptr.ToString(updatedConfig.ProductAnalytics),
		GrpcServerURL:        ptr.ToString(updatedConfig.GrpcServerURL),
		PostgresServerConfig: pgServerConfig,
	})
}

func parseMiscPayload(c *gin.Context) (*models.ServerMiscConfig, error) {
	var req openapi.ServerMiscConfig
	if err := c.ShouldBindJSON(&req); err != nil {
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
		return nil, fmt.Errorf("invalid attribute for 'grpc_server_url', it must start with 'grpc://', 'grpcs://', 'http://', or 'https://'")
	}

	var pgServerConfig *models.PostgresServerConfig
	if req.PostgresServerConfig != nil {
		if _, _, found := strings.Cut(req.PostgresServerConfig.ListenAddress, ":"); !found {
			return nil, fmt.Errorf("invalid attribute for 'listen_address', it must be in the format 'ip:port'")
		}
		pgServerConfig = &models.PostgresServerConfig{
			ListenAddress: req.PostgresServerConfig.ListenAddress,
		}
	}

	return &models.ServerMiscConfig{
		ProductAnalytics:     &req.ProductAnalytics,
		GrpcServerURL:        &req.GrpcServerURL,
		PostgresServerConfig: pgServerConfig,
	}, nil
}

func forbiddenOnMultiTenantSetups(c *gin.Context) (forbidden bool) {
	if appconfig.Get().OrgMultitenant() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "this operation is not allowed in multi-tenant mode"})
		return true
	}
	return false
}
