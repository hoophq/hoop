package apiserverinfo

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var (
	isOrgMultiTenant = os.Getenv("ORG_MULTI_TENANT") == "true"
	vinfo            = version.Get()
	serverInfoData   = openapi.ServerInfo{
		Version:                 vinfo.Version,
		Commit:                  vinfo.GitCommit,
		LogLevel:                os.Getenv("LOG_LEVEL"),
		GoDebug:                 os.Getenv("GODEBUG"),
		RedactProvider:          os.Getenv("DLP_PROVIDER"),
		HasWebhookAppKey:        isEnvSet("WEBHOOK_APPKEY"),
		HasIDPAudience:          isEnvSet("IDP_AUDIENCE"),
		HasIDPCustomScopes:      isEnvSet("IDP_CUSTOM_SCOPES"),
		DisableSessionsDownload: os.Getenv("DISABLE_SESSIONS_DOWNLOAD") == "true",
		// AnalyticsTracking:       getAnalyticsTrackingStatus(),
	}
)

// GetServerInfo
//
//	@Summary		Get Server Info
//	@Description	Get server information
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.ServerInfo
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/serverinfo [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		errMsg := fmt.Sprintf("failed obtaining organization, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	serverConfig, _, err := idp.LoadServerAuthConfig()
	if err != nil {
		log.Errorf("failed loading server auth config, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error, failed loading server auth config"})
		return
	}

	appc := appconfig.Get()
	apiHostname := appc.ApiHostname()
	l, licenseVerifyErr := defaultOSSLicense(), ""
	if org.LicenseData != nil {
		l, err = license.Parse(org.LicenseData, apiHostname)
		if err != nil {
			licenseVerifyErr = err.Error()
		}
	}

	serverInfoData.AnalyticsTracking = getAnalyticsTrackingStatus()
	if serverConfig != nil && serverConfig.ProductAnalytics != nil {
		serverInfoData.AnalyticsTracking = ptr.ToString(serverConfig.ProductAnalytics)
		switch serverInfoData.AnalyticsTracking {
		case "active":
			serverInfoData.AnalyticsTracking = string(openapi.AnalyticsTrackingEnabled)
		case "inactive":
			serverInfoData.AnalyticsTracking = string(openapi.AnalyticsTrackingDisabled)
		}
	}

	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}

	serverInfoData.IdpProviderName = parseIdpProviderName(serverConfig)
	serverInfoData.TenancyType = tenancyType
	serverInfoData.AuthMethod = string(ctx.ProviderType)
	serverInfoData.GrpcURL = ctx.GrpcURL
	serverInfoData.ApiURL = appc.ApiURL()
	serverInfoData.HasAskiAICredentials = appc.IsAskAIAvailable()
	serverInfoData.HasRedactCredentials = appc.HasRedactCredentials()
	serverInfoData.HasSSHClientHostKey = appc.SSHClientHostKey() != ""
	serverInfoData.LicenseInfo = &openapi.ServerLicenseInfo{
		IsValid:      err == nil,
		VerifyError:  licenseVerifyErr,
		VerifiedHost: apiHostname,
	}
	if l != nil {
		serverInfoData.LicenseInfo.KeyID = l.KeyID
		serverInfoData.LicenseInfo.AllowedHosts = l.Payload.AllowedHosts
		serverInfoData.LicenseInfo.Type = l.Payload.Type
		serverInfoData.LicenseInfo.IssuedAt = l.Payload.IssuedAt
		serverInfoData.LicenseInfo.ExpireAt = l.Payload.ExpireAt
	}
	c.JSON(http.StatusOK, serverInfoData)
}

func defaultOSSLicense() *license.License {
	return &license.License{
		KeyID: "",
		Payload: license.Payload{
			Type:         license.OSSType,
			IssuedAt:     time.Now().UTC().Unix(),
			ExpireAt:     time.Now().UTC().AddDate(10, 0, 0).Unix(),
			AllowedHosts: []string{"*"},
		},
	}
}

func isEnvSet(key string) bool {
	val, isset := os.LookupEnv(key)
	return isset && val != ""
}

func getAnalyticsTrackingStatus() string {
	if os.Getenv("ANALYTICS_TRACKING") == "disabled" {
		return string(openapi.AnalyticsTrackingDisabled)
	}
	return string(openapi.AnalyticsTrackingEnabled)
}

func parseIdpProviderName(conf *models.ServerAuthConfig) openapi.IdpProviderNameType {
	// default environment variable containing the issuer url
	issuerURL := os.Getenv("IDP_ISSUER")
	if issuerURL == "" {
		// optional environment variable containing the issuer url
		issuerURL = os.Getenv("IDP_URI")
	}
	// auth config from server takes precedence
	if conf != nil && conf.OidcConfig != nil {
		issuerURL = conf.OidcConfig.IssuerURL
	}
	u, _ := url.Parse(issuerURL)
	if u == nil {
		return openapi.IdpProviderUnknown
	}
	switch {
	case u.Hostname() == "accounts.google.com":
		return openapi.IdpProviderGoogle
	case u.Hostname() == "login.microsoftonline.com":
		return openapi.IdpProviderMicrosoftEntraID
	case strings.Contains(u.Hostname(), "okta.com"):
		return openapi.IdpProviderOkta
	case strings.Contains(u.Hostname(), "cognito"):
		return openapi.IdpProviderAwsCognito
	case strings.Contains(u.Hostname(), "jumpcloud"):
		return openapi.IdpProviderJumpCloud
	default:
		return openapi.IdpProviderUnknown
	}
}
