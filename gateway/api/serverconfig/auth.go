package apiserverconfig

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/idp"
	oidcprovider "github.com/hoophq/hoop/gateway/idp/oidc"
	samlprovider "github.com/hoophq/hoop/gateway/idp/saml"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const (
	defaultAdminRoleName   = "admin"
	defaultAuditorRoleName = "auditor"
	defaultGroupsClaim     = "groups"
)

// GetAuthConfig
//
//	@Summary		Get Authentication Configuration
//	@Description	Get authentication configuration
//	@Tags			Server Management
//	@Produce		json
//	@Success		200		{object}	openapi.ServerAuthConfig
//	@Failure		403,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/auth [get]
func GetAuthConfig(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}
	config, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		errMsg := fmt.Sprintf("failed to get server auth config, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}

	// return the current configuration from environment variables in case there's no auth config in the database
	isEmptyAuthConfig := err == models.ErrNotFound || config != nil && config.AuthMethod == nil
	if isEmptyAuthConfig {
		appc := appconfig.Get()
		authMethod := appc.AuthMethod()
		if authMethod == "idp" {
			authMethod = idptypes.ProviderTypeOIDC
		}
		webappUsersManagement := appc.WebappUsersManagement()
		if webappUsersManagement == "on" {
			webappUsersManagement = "active"
		} else {
			webappUsersManagement = "inactive"
		}

		var rolloutApiKey *string
		if config != nil && config.RolloutApiKey != nil {
			rolloutApiKey = config.RolloutApiKey
		}

		opts, err := idp.NewOidcProviderOptions(nil)
		if err != nil {
			log.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		var oidcConfig *models.ServerAuthOidcConfig
		if opts.IssuerURL != "" {
			scopes := []string{}
			if opts.CustomScopes != "" {
				scopes = append(scopes, strings.Split(opts.CustomScopes, ",")...)
			}
			oidcConfig = &models.ServerAuthOidcConfig{
				IssuerURL:    opts.IssuerURL,
				ClientID:     opts.ClientID,
				ClientSecret: opts.ClientSecret,
				Scopes:       scopes,
				Audience:     opts.Audience,
				GroupsClaim:  opts.GroupsClaim,
			}
		}
		config = &models.ServerAuthConfig{
			AuthMethod:            ptr.String(string(authMethod)),
			OidcConfig:            oidcConfig,
			SamlConfig:            nil,
			ApiKey:                nil,
			ProviderName:          nil,
			RolloutApiKey:         rolloutApiKey,
			WebappUsersManagement: ptr.String(webappUsersManagement),
			AdminRoleName:         ptr.String(types.GroupAdmin),
			AuditorRoleName:       ptr.String(types.GroupAuditor),
			ProductAnalytics:      nil,
			GrpcServerURL:         nil,
			SharedSigningKey:      nil,
			UpdatedAt:             time.Time{},
		}
	}
	c.JSON(http.StatusOK, toAuthOpenApi(config))
}

// UpdateAuthConfig
//
//	@Summary		Update Authentication Configuration
//	@Description	Update authentication configuration
//	@Tags			Server Management
//	@Param			request	body	openapi.ServerAuthConfig	true	"The request body resource"
//	@Accept			json
//	@Produce		json
//	@Success		200				{object}	openapi.ServerAuthConfig
//	@Failure		400,403,422,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/auth [put]
func UpdateAuthConfig(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}
	ctx := storagev2.ParseContext(c)
	req, err := parseRequestAuthPayload(c)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	existentConfig, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existentConfig == nil {
		existentConfig = &models.ServerAuthConfig{}
	}

	existentConfig.OrgID = ctx.OrgID
	var existentRolloutApiKey string
	if existentConfig.RolloutApiKey != nil {
		existentRolloutApiKey = *existentConfig.RolloutApiKey
	}

	if req.RolloutApiKey != nil {
		if *req.RolloutApiKey != existentRolloutApiKey {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"message": "rollout api key does match with stored value, use the generate api key endpoint to create a new key"})
			return
		}
		existentConfig.ApiKey = req.RolloutApiKey
		existentConfig.RolloutApiKey = nil
	}

	existentConfig.OidcConfig = nil
	if req.OidcConfig != nil {
		existentConfig.OidcConfig = &models.ServerAuthOidcConfig{
			IssuerURL:    req.OidcConfig.IssuerURL,
			ClientID:     req.OidcConfig.ClientID,
			ClientSecret: req.OidcConfig.ClientSecret,
			Scopes:       req.OidcConfig.Scopes,
			Audience:     req.OidcConfig.Audience,
			GroupsClaim:  req.OidcConfig.GroupsClaim,
		}
	}

	existentConfig.SamlConfig = nil
	if req.SamlConfig != nil {
		existentConfig.SamlConfig = &models.ServerAuthSamlConfig{
			IdpMetadataURL: req.SamlConfig.IdpMetadataURL,
			GroupsClaim:    req.SamlConfig.GroupsClaim,
		}
	}

	adminRoleName := defaultAdminRoleName
	if req.AdminRoleName != "" {
		adminRoleName = req.AdminRoleName
	}
	auditorRoleName := defaultAuditorRoleName
	if req.AuditorRoleName != "" {
		auditorRoleName = req.AuditorRoleName
	}
	existentConfig.AdminRoleName = &adminRoleName
	existentConfig.AuditorRoleName = &auditorRoleName
	existentConfig.AuthMethod = ptr.String(string(req.AuthMethod))
	existentConfig.WebappUsersManagement = ptr.String(req.WebappUsersManagementStatus)
	existentConfig.ProviderName = ptr.String(req.ProviderName)

	log.With("auth_method", req.AuthMethod).Infof("performing pre-flight check for auth config update")
	switch req.AuthMethod {
	case openapi.ProviderTypeOIDC:
		_, err = oidcprovider.New(oidcprovider.Options{
			IssuerURL:    req.OidcConfig.IssuerURL,
			ClientID:     req.OidcConfig.ClientID,
			ClientSecret: req.OidcConfig.ClientSecret,
			Audience:     req.OidcConfig.Audience,
			GroupsClaim:  req.OidcConfig.GroupsClaim,
			CustomScopes: strings.Join(req.OidcConfig.Scopes, ","),
		})
	case openapi.ProviderTypeSAML:
		_, err = samlprovider.New(samlprovider.Options{
			IdpMetadataURL: req.SamlConfig.IdpMetadataURL,
			GroupsClaim:    req.SamlConfig.GroupsClaim,
		})
	case openapi.ProviderTypeLocal: // noop
		err = nil
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("invalid auth method: %s", req.AuthMethod)})
		return
	}
	if err != nil {
		log.Warnf("failed to create IDP provider for auth config update, reason=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resp, err := models.UpdateServerAuthConfig(existentConfig)
	audit.LogFromContextErr(c, audit.ResourceAuthConfig, audit.ActionUpdate, "", "", payloadAuthConfigUpdate(string(req.AuthMethod), adminRoleName, auditorRoleName, req.ProviderName), err)
	if err != nil {
		log.Errorf("failed to update server auth config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to update server auth config"})
		return
	}

	// in the future, refactor to propagate the roles as context to routes
	setGlobalGatewayUserRoles(resp)

	c.JSON(http.StatusOK, toAuthOpenApi(resp))
}

// GenerateApiKey
//
//	@Summary		Generate API Key
//	@Description	Generate a rollout api key
//	@Tags			Server Management
//	@Produce		json
//	@Success		201		{object}	openapi.GenerateApiKeyResponse
//	@Failure		403,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/auth/apikey [post]
func GenerateApiKey(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}
	ctx := storagev2.ParseContext(c)
	rolloutKey, _, err := keys.GenerateSecureRandomKey("xapi", 64)
	if err != nil {
		errMsg := fmt.Sprintf("failed generating API key, err=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}

	existentConfig, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		log.Errorf("failed to get server auth config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to get server auth config"})
		return
	}
	if existentConfig == nil {
		existentConfig = &models.ServerAuthConfig{}
	}

	existentConfig.OrgID = ctx.OrgID
	existentConfig.RolloutApiKey = ptr.String(rolloutKey)
	if _, err := models.UpdateServerAuthConfig(existentConfig); err != nil {
		log.Errorf("failed updating rollout api key , reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating rollout api key"})
		return
	}
	c.JSON(http.StatusCreated, openapi.GenerateApiKeyResponse{RolloutApiKey: rolloutKey})
}

// SetGlobalGatewayUserRoles sets the global admin and auditor roles based on the server auth config.
// This function should be called during the initialization of the gateway to ensure that the roles are set correctly
func SetGlobalGatewayUserRoles() error {
	serverConfig, err := models.GetServerAuthConfig()
	switch err {
	case models.ErrNotFound:
	case nil:
		setGlobalGatewayUserRoles(serverConfig)
	default:
		return fmt.Errorf("failed to get server auth config: %v", err)
	}

	return nil
}

func setGlobalGatewayUserRoles(serverConfig *models.ServerAuthConfig) {
	if serverConfig.AdminRoleName != nil && types.GroupAdmin != *serverConfig.AdminRoleName {
		log.Warnf("changing global admin role name from %s to %s", types.GroupAdmin, *serverConfig.AdminRoleName)
		types.GroupAdmin = *serverConfig.AdminRoleName
	}
	if serverConfig.AuditorRoleName != nil && types.GroupAuditor != *serverConfig.AuditorRoleName {
		log.Warnf("changing global auditor role name from %s to %s", types.GroupAuditor, *serverConfig.AuditorRoleName)
		types.GroupAuditor = *serverConfig.AuditorRoleName
	}
}

func parseRequestAuthPayload(c *gin.Context) (*openapi.ServerAuthConfig, error) {
	var req openapi.ServerAuthConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, err
	}

	invalidStatus := req.WebappUsersManagementStatus != "active" && req.WebappUsersManagementStatus != "inactive"
	if invalidStatus {
		return nil, fmt.Errorf("invalid attribute for webapp_users_management_status, accepted values are 'active' or 'inactive'")
	}

	if req.AuthMethod == openapi.ProviderTypeOIDC && req.OidcConfig == nil {
		return nil, fmt.Errorf("attribute 'oidc_config' is required for OIDC auth method")
	}

	if req.AuthMethod == openapi.ProviderTypeSAML && req.SamlConfig == nil {
		return nil, fmt.Errorf("attribute 'saml_config' is required for SAML auth method")
	}

	if req.OidcConfig != nil {
		if req.OidcConfig.GroupsClaim == "" {
			req.OidcConfig.GroupsClaim = defaultGroupsClaim
		}
	}

	if req.SamlConfig != nil {
		if req.SamlConfig.GroupsClaim == "" {
			req.SamlConfig.GroupsClaim = defaultGroupsClaim
		}
	}
	return &req, nil
}

func toAuthOpenApi(cfg *models.ServerAuthConfig) *openapi.ServerAuthConfig {
	if cfg == nil {
		return &openapi.ServerAuthConfig{}
	}
	var oidcConfig *openapi.ServerAuthOidcConfig
	if cfg.OidcConfig != nil {
		oidcConfig = &openapi.ServerAuthOidcConfig{
			IssuerURL:    cfg.OidcConfig.IssuerURL,
			ClientID:     cfg.OidcConfig.ClientID,
			ClientSecret: cfg.OidcConfig.ClientSecret,
			Scopes:       cfg.OidcConfig.Scopes,
			Audience:     cfg.OidcConfig.Audience,
			GroupsClaim:  cfg.OidcConfig.GroupsClaim,
		}
	}
	var samlConfig *openapi.ServerAuthSamlConfig
	if cfg.SamlConfig != nil {
		samlConfig = &openapi.ServerAuthSamlConfig{
			IdpMetadataURL: cfg.SamlConfig.IdpMetadataURL,
			GroupsClaim:    cfg.SamlConfig.GroupsClaim,
		}
	}
	return &openapi.ServerAuthConfig{
		AuthMethod:                  openapi.ProviderType(ptr.ToString(cfg.AuthMethod)),
		OidcConfig:                  oidcConfig,
		SamlConfig:                  samlConfig,
		ProviderName:                ptr.ToString(cfg.ProviderName),
		ApiKey:                      cfg.ApiKey,
		RolloutApiKey:               cfg.RolloutApiKey,
		WebappUsersManagementStatus: ptr.ToString(cfg.WebappUsersManagement),
		AdminRoleName:               ptr.ToString(cfg.AdminRoleName),
		AuditorRoleName:             ptr.ToString(cfg.AuditorRoleName),
	}
}

func payloadAuthConfigUpdate(authMethod, adminRoleName, auditorRoleName, providerName string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"auth_method": authMethod, "admin_role_name": adminRoleName, "auditor_role_name": auditorRoleName, "provider_name": providerName}
	}
}
