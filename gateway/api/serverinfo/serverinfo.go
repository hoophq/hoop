package apiserverinfo

import (
	"fmt"
	"libhoop/log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/license"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/appconfig"
	pgorgs "github.com/runopsio/hoop/gateway/pgrest/orgs"
	"github.com/runopsio/hoop/gateway/storagev2"
)

var (
	isOrgMultiTenant = os.Getenv("ORG_MULTI_TENANT") == "true"
	vinfo            = version.Get()
	serverInfoData   = map[string]any{
		"version":                vinfo.Version,
		"gateway_commit":         vinfo.GitCommit,
		"webapp_commit":          appruntime.WebAppCommit,
		"log_level":              os.Getenv("LOG_LEVEL"),
		"go_debug":               os.Getenv("GODEBUG"),
		"admin_username":         os.Getenv("ADMIN_USERNAME"),
		"has_redact_credentials": isEnvSet("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		"has_webhook_app_key":    isEnvSet("WEBHOOK_APPKEY"),
		"has_idp_audience":       isEnvSet("IDP_AUDIENCE"),
		"has_idp_custom_scopes":  isEnvSet("IDP_CUSTOM_SCOPES"),
		"has_postgrest_role":     isEnvSet("PGREST_ROLE"),
	}
)

type handler struct {
	grpcURL string
}

func New(grpcURL string) *handler {
	return &handler{grpcURL: grpcURL}
}

func (h *handler) Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	org, err := pgorgs.New().FetchOrgByContext(ctx)
	if err != nil || org == nil {
		errMsg := fmt.Sprintf("failed obtaining organization license, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	apiHostname := appconfig.Get().ApiHostname()
	l, err := license.Parse(org.LicenseData, apiHostname)
	licenseVerifyErr := ""
	if err != nil {
		licenseVerifyErr = err.Error()
	}
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	serverInfoData["tenancy_type"] = tenancyType
	serverInfoData["grpc_url"] = h.grpcURL
	serverInfoData["has_ask_ai_credentials"] = appconfig.Get().IsAskAIAvailable()
	serverInfoData["license_info"] = nil
	if l != nil {
		serverInfoData["license_info"] = map[string]any{
			"key_id":        l.KeyID,
			"allowed_hosts": l.Payload.AllowedHosts,
			"type":          l.Payload.Type,
			"issued_at":     fmt.Sprintf("%v", l.Payload.IssuedAt),
			"expire_at":     fmt.Sprintf("%v", l.Payload.ExpireAt),
			"is_valid":      err == nil,
			"verify_error":  licenseVerifyErr,
			"verified_host": apiHostname,
		}
	}
	c.JSON(http.StatusOK, serverInfoData)
}

func isEnvSet(key string) bool {
	val, isset := os.LookupEnv(key)
	return isset && val != ""
}
