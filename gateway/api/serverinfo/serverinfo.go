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
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var (
	isOrgMultiTenant = os.Getenv("ORG_MULTI_TENANT") == "true"
	vinfo            = version.Get()
	serverInfoData   = openapi.ServerInfo{
		Version:              vinfo.Version,
		Commit:               vinfo.GitCommit,
		LogLevel:             os.Getenv("LOG_LEVEL"),
		GoDebug:              os.Getenv("GODEBUG"),
		AdminUsername:        os.Getenv("ADMIN_USERNAME"),
		AuthMethod:           appconfig.Get().AuthMethod(),
		HasRedactCredentials: isEnvSet("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		HasWebhookAppKey:     isEnvSet("WEBHOOK_APPKEY"),
		HasIDPAudience:       isEnvSet("IDP_AUDIENCE"),
		HasIDPCustomScopes:   isEnvSet("IDP_CUSTOM_SCOPES"),
		HasPostgresRole:      isEnvSet("PGREST_ROLE"),
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
	org, err := pgorgs.New().FetchOrgByContext(ctx)
	if err != nil || org == nil {
		errMsg := fmt.Sprintf("failed obtaining organization, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	apiHostname := appconfig.Get().ApiHostname()
	l, licenseVerifyErr := defaultOSSLicense(), ""
	if org.LicenseData != nil {
		l, err = license.Parse(*org.LicenseData, apiHostname)
		if err != nil {
			licenseVerifyErr = err.Error()
		}
	}
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	serverInfoData.TenancyType = tenancyType
	serverInfoData.GrpcURL = h.grpcURL
	serverInfoData.ApiURL = appconfig.Get().ApiURL()
	serverInfoData.HasAskiAICredentials = appconfig.Get().IsAskAIAvailable()
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
