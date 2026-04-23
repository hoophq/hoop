package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type serverInfoGetInput struct{}

func registerServerInfoTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "serverinfo_get",
		Description: "Get gateway server information including version, auth method, license info, and feature flags",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, serverInfoGetHandler)
}

func serverInfoGetHandler(ctx context.Context, _ *mcp.CallToolRequest, _ serverInfoGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	org, err := models.GetOrganizationByNameOrID(sc.OrgID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching organization: %w", err)
	}

	serverConfig, _, err := idp.LoadServerAuthConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed loading server auth config: %w", err)
	}

	appc := appconfig.Get()
	apiHostname := appc.ApiHostname()

	vinfo := version.Get()

	tenancyType := "selfhosted"
	if os.Getenv("ORG_MULTI_TENANT") == "true" {
		tenancyType = "multitenant"
	}

	result := map[string]any{
		"version":      vinfo.Version,
		"commit":       vinfo.GitCommit,
		"auth_method":  string(sc.ProviderType),
		"api_url":      appc.ApiURL(),
		"grpc_url":     sc.GrpcURL,
		"tenancy_type": tenancyType,
	}

	if serverConfig != nil {
		result["idp_provider_name"] = parseIdpProvider(serverConfig)
	}

	// License info
	licenseInfo := map[string]any{
		"is_valid": true,
	}

	if org.LicenseData != nil {
		l, parseErr := license.Parse(org.LicenseData, apiHostname)
		if parseErr != nil {
			licenseInfo["is_valid"] = false
			licenseInfo["verify_error"] = parseErr.Error()
		}
		if l != nil {
			licenseInfo["key_id"] = l.KeyID
			licenseInfo["type"] = l.Payload.Type
			licenseInfo["issued_at"] = l.Payload.IssuedAt
			licenseInfo["expire_at"] = l.Payload.ExpireAt
			licenseInfo["allowed_hosts"] = l.Payload.AllowedHosts
		}
	} else {
		// Default OSS license
		licenseInfo["type"] = license.OSSType
		licenseInfo["issued_at"] = time.Now().UTC().Unix()
		licenseInfo["expire_at"] = time.Now().UTC().AddDate(10, 0, 0).Unix()
		licenseInfo["allowed_hosts"] = []string{"*"}
	}

	result["license_info"] = licenseInfo

	return jsonResult(result)
}

func parseIdpProvider(conf *models.ServerAuthConfig) string {
	issuerURL := os.Getenv("IDP_ISSUER")
	if issuerURL == "" {
		issuerURL = os.Getenv("IDP_URI")
	}
	if conf != nil && conf.OidcConfig != nil {
		issuerURL = conf.OidcConfig.IssuerURL
	}
	if issuerURL == "" {
		return "unknown"
	}

	// Simple heuristic based on issuer URL hostname
	switch {
	case strings.Contains(issuerURL, "accounts.google.com"):
		return "google"
	case strings.Contains(issuerURL, "login.microsoftonline.com"):
		return "microsoft"
	case strings.Contains(issuerURL, "okta.com"):
		return "okta"
	case strings.Contains(issuerURL, "cognito"):
		return "cognito"
	case strings.Contains(issuerURL, "jumpcloud"):
		return "jumpcloud"
	default:
		return "unknown"
	}
}
