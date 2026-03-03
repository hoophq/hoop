package analytics

import (
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
)

var (
	segmentApiKey   string
	intercomHmacKey string
)

func IsAnalyticsEnabled() (bool, error) {
	analyticsEnabled := true

	loadServerConfig, err := loadServerConfig()
	if err != nil {
		return analyticsEnabled, err
	}

	if loadServerConfig != nil && loadServerConfig.ProductAnalytics != nil {
		analyticsEnabled = *loadServerConfig.ProductAnalytics == "active"
	}

	return analyticsEnabled, nil
}

func loadServerConfig() (serverConfig *models.ServerMiscConfig, err error) {
	serverConfig, err = models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed to get server config, reason=%v", err)
	}

	return serverConfig, nil
}
