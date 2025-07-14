package apiserverinfo

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
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
		AdminUsername:           os.Getenv("ADMIN_USERNAME"),
		RedactProvider:          os.Getenv("DLP_PROVIDER"),
		HasWebhookAppKey:        isEnvSet("WEBHOOK_APPKEY"),
		HasIDPAudience:          isEnvSet("IDP_AUDIENCE"),
		HasIDPCustomScopes:      isEnvSet("IDP_CUSTOM_SCOPES"),
		DisableSessionsDownload: os.Getenv("DISABLE_SESSIONS_DOWNLOAD") == "true",
		AnalyticsTracking:       getAnalyticsTrackingStatus(),
	}
)

type handler struct {
	grpcURL string
}

func New(grpcURL string) *handler { return &handler{grpcURL: grpcURL} }

// GetServerInfo
//
//	@Summary		Get Server Info
//	@Description	Get server information
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.ServerInfo
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/serverinfo [get]
func (h *handler) Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		errMsg := fmt.Sprintf("failed obtaining organization, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
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
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	serverInfoData.AuthMethod = string(appc.AuthMethod())
	serverInfoData.TenancyType = tenancyType
	serverInfoData.GrpcURL = h.grpcURL
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
