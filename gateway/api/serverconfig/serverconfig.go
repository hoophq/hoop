package apiserverconfig

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

func Get(c *gin.Context) {
	config, err := models.GetServerConfig()
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "server config not found"})
		return
	case nil:
		c.JSON(http.StatusOK, openapi.ServerConfig{
			ProductAnalytics:      config.ProductAnalytics,
			WebappUsersManagement: config.WebappUsersManagement,
			GrpcServerURL:         config.GrpcServerURL,
		})
		return
	default:
		log.Errorf("failed to get server config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get server config"})
		return
	}
}

func Put(c *gin.Context) {
	req, err := parsePayload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	updatedConfig, err := models.UpsertServerConfig(&models.ServerConfig{
		ProductAnalytics:      req.ProductAnalytics,
		WebappUsersManagement: req.WebappUsersManagement,
		GrpcServerURL:         req.GrpcServerURL,
	})
	if err != nil {
		log.Errorf("failed to update server config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update server config"})
		return
	}

	c.JSON(http.StatusOK, openapi.ServerConfig{
		ProductAnalytics:      updatedConfig.ProductAnalytics,
		WebappUsersManagement: updatedConfig.WebappUsersManagement,
		GrpcServerURL:         updatedConfig.GrpcServerURL,
	})
}

func parsePayload(c *gin.Context) (*models.ServerConfig, error) {
	var req openapi.ServerConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed to bind request, reason=%v", err)
		return nil, err
	}

	invalidStatus := req.ProductAnalytics != "active" && req.ProductAnalytics != "inactive"
	if invalidStatus {
		return nil, fmt.Errorf("invalid attribute for product_analytics, accepted values are 'active' or 'inactive'")
	}

	invalidStatus = req.WebappUsersManagement != "active" && req.WebappUsersManagement != "inactive"
	if invalidStatus {
		return nil, fmt.Errorf("invalid attribute for webapp_users_management, accepted values are 'active' or 'inactive'")
	}

	return &models.ServerConfig{
		ProductAnalytics:      req.ProductAnalytics,
		WebappUsersManagement: req.WebappUsersManagement,
		GrpcServerURL:         req.GrpcServerURL,
	}, nil
}
