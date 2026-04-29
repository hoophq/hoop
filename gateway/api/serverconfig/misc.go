package apiserverconfig

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/httpproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/postgresproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy"
	"github.com/hoophq/hoop/gateway/rdp"
)

const defaultGrpcServerURL = "grpc://127.0.0.1:8010"

var errListenAddrFormat = errors.New("invalid attribute for 'listen_address', it must be in the format 'ip:port'")

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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get server config: %v", err)
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

	var sshServerConfig *openapi.SSHServerConfig
	if config.SSHServerConfig != nil {
		sshServerConfig = &openapi.SSHServerConfig{
			ListenAddress: config.SSHServerConfig.ListenAddress,
			HostsKey:      config.SSHServerConfig.HostsKey,
		}
	}
	var rdpServerConfig *openapi.RDPServerConfig
	if config.RDPServerConfig != nil {
		rdpServerConfig = &openapi.RDPServerConfig{
			ListenAddress: config.RDPServerConfig.ListenAddress,
		}
	}

	var httpProxyServerConfig *openapi.HttpProxyServerConfig
	if config.HttpProxyServerConfig != nil {
		httpProxyServerConfig = &openapi.HttpProxyServerConfig{
			ListenAddress: config.HttpProxyServerConfig.ListenAddress,
		}
	}
	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics:      productAnalytics,
		GrpcServerURL:         grpcURL,
		PostgresServerConfig:  pgServerConfig,
		SSHServerConfig:       sshServerConfig,
		RDPServerConfig:       rdpServerConfig,
		HttpProxyServerConfig: httpProxyServerConfig,
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

	globalConfig := appconfig.Get()
	tlsConfig, err := globalConfig.GetTLSConfig()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	newState, err := parseMiscPayload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	currentSrvConf, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get server config: %v", err)
		return
	}

	//rdp server
	rdpInstance := rdp.GetServerInstance()
	rdpConf, state := parserRdpsConfigState(currentSrvConf, newState)
	switch state {
	case instanceStateStart:
		_ = rdpInstance.Stop()
		err = rdpInstance.Start(rdpConf.ListenAddress, tlsConfig, globalConfig.GatewayAllowPlaintext())
	case instanceStateStop:
		err = rdpInstance.Stop()
	}

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed handling rdp server startup: %v", err)
		return
	}

	pgInstance := postgresproxy.GetServerInstance()
	pgConf, state := parsePostgresConfigState(currentSrvConf, newState)
	switch state {
	case instanceStateStart:
		_ = pgInstance.Stop()
		err = pgInstance.Start(pgConf.ListenAddress, tlsConfig)
	case instanceStateStop:
		err = pgInstance.Stop()
	}

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed handling postgres server startup: %v", err)
		return
	}

	sshInstance := sshproxy.GetServerInstance()
	sshConf, state := parseSSHConfigState(currentSrvConf, newState)
	switch state {
	case instanceStateStart:
		if sshConf.HostsKey == "" {
			log.Infof("generating a new ed25519 hosts key")
			privateKeyPemBytes, err := newEd25519PrivateKey()
			if err != nil {
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to generate hosts key: %v", err)
				return
			}
			sshConf.HostsKey = base64.StdEncoding.EncodeToString(privateKeyPemBytes)
			newState.SSHServerConfig.HostsKey = sshConf.HostsKey
		}

		_ = sshInstance.Stop()
		err = sshInstance.Start(sshConf.ListenAddress, sshConf.HostsKey)
	case instanceStateStop:
		err = sshInstance.Stop()
	}

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed handling ssh server startup: %v", err)
		return
	}

	httpProxyInstance := httpproxy.GetServerInstance()
	httpProxyConf, state := parseHttpProxyConfigState(currentSrvConf, newState)
	switch state {
	case instanceStateStart:
		_ = httpProxyInstance.Stop()
		err = httpProxyInstance.Start(httpProxyConf.ListenAddress, tlsConfig)
	case instanceStateStop:
		err = httpProxyInstance.Stop()
	}

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed handling http proxy server startup: %v", err)
		return
	}

	evt := audit.NewEvent(audit.ResourceServerConfig, audit.ActionUpdate).
		Set("product_analytics", ptr.ToString(newState.ProductAnalytics)).
		Set("grpc_server_url", ptr.ToString(newState.GrpcServerURL)).
		Set("postgres_server_config", newState.PostgresServerConfig).
		Set("ssh_server_config", newState.SSHServerConfig).
		Set("rdp_server_config", newState.RDPServerConfig).
		Set("http_proxy_server_config", newState.HttpProxyServerConfig)
	defer func() { evt.Log(c) }()

	updatedConfig, err := models.UpsertServerMiscConfig(&models.ServerMiscConfig{
		ProductAnalytics:      newState.ProductAnalytics,
		GrpcServerURL:         newState.GrpcServerURL,
		PostgresServerConfig:  newState.PostgresServerConfig,
		SSHServerConfig:       newState.SSHServerConfig,
		RDPServerConfig:       newState.RDPServerConfig,
		HttpProxyServerConfig: newState.HttpProxyServerConfig,
	})
	if err != nil {
		evt.Err(err)
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update server config")
		return
	}

	updateAnalyticsTracking(newState.ProductAnalytics)

	var pgServerConfig *openapi.PostgresServerConfig
	if updatedConfig.PostgresServerConfig != nil {
		pgServerConfig = &openapi.PostgresServerConfig{
			ListenAddress: updatedConfig.PostgresServerConfig.ListenAddress,
		}
	}

	var sshServerConfig *openapi.SSHServerConfig
	if updatedConfig.SSHServerConfig != nil {
		sshServerConfig = &openapi.SSHServerConfig{
			ListenAddress: updatedConfig.SSHServerConfig.ListenAddress,
			HostsKey:      updatedConfig.SSHServerConfig.HostsKey,
		}
	}

	var rdpServerConfig *openapi.RDPServerConfig
	if updatedConfig.RDPServerConfig != nil {
		rdpServerConfig = &openapi.RDPServerConfig{
			ListenAddress: updatedConfig.RDPServerConfig.ListenAddress,
		}
	}
	var httpProxyServerConfig *openapi.HttpProxyServerConfig
	if updatedConfig.HttpProxyServerConfig != nil {
		httpProxyServerConfig = &openapi.HttpProxyServerConfig{
			ListenAddress: updatedConfig.HttpProxyServerConfig.ListenAddress,
		}
	}
	c.JSON(http.StatusOK, openapi.ServerMiscConfig{
		ProductAnalytics:      ptr.ToString(updatedConfig.ProductAnalytics),
		GrpcServerURL:         ptr.ToString(updatedConfig.GrpcServerURL),
		PostgresServerConfig:  pgServerConfig,
		SSHServerConfig:       sshServerConfig,
		RDPServerConfig:       rdpServerConfig,
		HttpProxyServerConfig: httpProxyServerConfig,
	})
}

func parseMiscPayload(c *gin.Context) (*models.ServerMiscConfig, error) {
	var req openapi.ServerMiscConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, fmt.Errorf("invalid request body, err=%v", err)
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

	// postgres server configuration attribute
	var pgServerConfig *models.PostgresServerConfig
	if req.PostgresServerConfig != nil {
		if _, _, found := strings.Cut(req.PostgresServerConfig.ListenAddress, ":"); req.PostgresServerConfig.ListenAddress != "" && !found {
			return nil, errListenAddrFormat
		}
		pgServerConfig = &models.PostgresServerConfig{
			ListenAddress: req.PostgresServerConfig.ListenAddress,
		}
	}

	// ssh server configuration attribute
	var sshServerConfig *models.SSHServerConfig
	if req.SSHServerConfig != nil {
		if _, _, found := strings.Cut(req.SSHServerConfig.ListenAddress, ":"); req.SSHServerConfig.ListenAddress != "" && !found {
			return nil, errListenAddrFormat
		}
		sshServerConfig = &models.SSHServerConfig{
			ListenAddress: req.SSHServerConfig.ListenAddress,
			HostsKey:      req.SSHServerConfig.HostsKey,
		}
	}

	//rdp server configuration attribute
	var rdpServerConfig *models.RDPServerConfig
	if req.RDPServerConfig != nil {
		if _, _, found := strings.Cut(req.RDPServerConfig.ListenAddress, ":"); req.RDPServerConfig.ListenAddress != "" && !found {
			return nil, errListenAddrFormat
		}
		rdpServerConfig = &models.RDPServerConfig{
			ListenAddress: req.RDPServerConfig.ListenAddress,
		}
	}

	// http proxy server configuration attribute
	var httpProxyServerConfig *models.HttpProxyServerConfig
	if req.HttpProxyServerConfig != nil {
		if _, _, found := strings.Cut(req.HttpProxyServerConfig.ListenAddress, ":"); req.HttpProxyServerConfig.ListenAddress != "" && !found {
			return nil, errListenAddrFormat
		}
		httpProxyServerConfig = &models.HttpProxyServerConfig{
			ListenAddress: req.HttpProxyServerConfig.ListenAddress,
		}
	}

	return &models.ServerMiscConfig{
		ProductAnalytics:      &req.ProductAnalytics,
		GrpcServerURL:         &req.GrpcServerURL,
		PostgresServerConfig:  pgServerConfig,
		SSHServerConfig:       sshServerConfig,
		RDPServerConfig:       rdpServerConfig,
		HttpProxyServerConfig: httpProxyServerConfig,
	}, nil
}

func forbiddenOnMultiTenantSetups(c *gin.Context) (forbidden bool) {
	if appconfig.Get().OrgMultitenant() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "this operation is not allowed in multi-tenant mode"})
		return true
	}
	return false
}

type instanceState string

var (
	instanceStateStart instanceState = "start"
	instanceStateStop  instanceState = "stop"
)

func parsePostgresConfigState(currentState, newState *models.ServerMiscConfig) (newConf models.PostgresServerConfig, state instanceState) {
	var currentConf models.PostgresServerConfig
	if currentState != nil && currentState.PostgresServerConfig != nil {
		currentConf = *currentState.PostgresServerConfig
	}

	if newState != nil && newState.PostgresServerConfig != nil {
		newConf = *newState.PostgresServerConfig
	}

	switch {
	// stop instance when new configuration is empty
	case newConf.ListenAddress == "":
		return newConf, "stop"
	// restart on configuration drift
	case currentConf.ListenAddress != newConf.ListenAddress:
		return newConf, "start"
	// noop, no configuration drift
	default:
		return
	}
}

func parserRdpsConfigState(currentState, newState *models.ServerMiscConfig) (newConf models.RDPServerConfig, state instanceState) {
	var currentConf models.RDPServerConfig
	if currentState != nil && currentState.RDPServerConfig != nil {
		currentConf = *currentState.RDPServerConfig
	}
	if newState != nil && newState.RDPServerConfig != nil {
		newConf = *newState.RDPServerConfig
	}

	switch {
	// stop instance when new configuration is empty
	case newConf.ListenAddress == "":
		return newConf, "stop"
	// restart on configuration drift
	case currentConf.ListenAddress != newConf.ListenAddress:
		return newConf, "start"
	// noop, no configuration drift
	default:
		return
	}
}

func parseSSHConfigState(currentState, newState *models.ServerMiscConfig) (newConf models.SSHServerConfig, state instanceState) {
	var currentConf models.SSHServerConfig
	if currentState != nil && currentState.SSHServerConfig != nil {
		currentConf = *currentState.SSHServerConfig
	}
	if newState != nil && newState.SSHServerConfig != nil {
		newConf = *newState.SSHServerConfig
	}

	switch {
	// stop instance when new configuration is empty
	case newConf.ListenAddress == "":
		return newConf, "stop"
	// restart on configuration drift
	case currentConf.ListenAddress != newConf.ListenAddress,
		currentConf.HostsKey != newConf.HostsKey:
		return newConf, "start"
	// noop, no configuration drift
	default:
		return
	}
}

func parseHttpProxyConfigState(currentState, newState *models.ServerMiscConfig) (newConf models.HttpProxyServerConfig, state instanceState) {
	var currentConf models.HttpProxyServerConfig
	if currentState != nil && currentState.HttpProxyServerConfig != nil {
		currentConf = *currentState.HttpProxyServerConfig
	}
	if newState != nil && newState.HttpProxyServerConfig != nil {
		newConf = *newState.HttpProxyServerConfig
	}

	switch {
	// stop instance when new configuration is empty
	case newConf.ListenAddress == "":
		return newConf, "stop"
	// restart on configuration drift
	case currentConf.ListenAddress != newConf.ListenAddress:
		return newConf, "start"
	// noop, no configuration drift
	default:
		return
	}
}

func newEd25519PrivateKey() (privateKey []byte, err error) {
	_, privKey, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}
	return sshproxy.EncodePrivateKeyToOpenSSH(privKey)
}

func updateAnalyticsTracking(newState *string) {
	appconfig.GetRef().SetAnalyticsTracking(newState == nil || *newState == "active")
}
