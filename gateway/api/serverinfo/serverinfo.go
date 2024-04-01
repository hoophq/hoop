package apiserverinfo

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/version"
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
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	serverInfoData["tenancy_type"] = tenancyType
	serverInfoData["grpc_url"] = h.grpcURL
	c.JSON(http.StatusOK, serverInfoData)
}

func isEnvSet(key string) bool {
	val, isset := os.LookupEnv(key)
	return isset && val != ""
}
