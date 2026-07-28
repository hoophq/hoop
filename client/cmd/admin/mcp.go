package admin

import (
	"fmt"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/spf13/cobra"
)

const mcpAuthEndpoint = "/api/serverconfig/mcp-auth"

var (
	mcpAuthResourceURIFlag  string
	mcpAuthGroupsClaimFlag  string
	mcpAuthClientIDFlag     string
	mcpAuthClientSecretFlag string
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP (Model Context Protocol) gateway settings",
}

var mcpAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Configure OAuth 2.1 Resource Server authentication for the /api/mcp endpoint",
	Long: `Configure OAuth 2.1 Resource Server authentication for the Hoop MCP endpoint.

When enabled, the /api/mcp endpoint accepts JWTs issued by the configured OIDC
issuer (in addition to gateway Hoop bearer tokens). The audience claim of inbound
JWTs must match the configured resource URI.`,
}

var mcpAuthEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable OAuth 2.1 authentication on the MCP endpoint",
	Run: func(cmd *cobra.Command, args []string) {
		current := mcpAuthGetOrEmpty()
		current["enabled"] = true
		if mcpAuthResourceURIFlag != "" {
			current["resource_uri"] = mcpAuthResourceURIFlag
		}
		if mcpAuthGroupsClaimFlag != "" {
			current["groups_claim"] = mcpAuthGroupsClaimFlag
		}
		if mcpAuthClientIDFlag != "" {
			current["client_id"] = mcpAuthClientIDFlag
		}
		if mcpAuthClientSecretFlag != "" {
			current["client_secret"] = mcpAuthClientSecretFlag
		}
		mcpAuthPutOrDie(current)
		fmt.Println(styles.Fainted("MCP OAuth authentication enabled."))
		mcpAuthPrintStatus(current)
	},
}

var mcpAuthDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable OAuth 2.1 authentication on the MCP endpoint",
	Run: func(cmd *cobra.Command, args []string) {
		current := mcpAuthGetOrEmpty()
		current["enabled"] = false
		mcpAuthPutOrDie(current)
		fmt.Println(styles.Fainted("MCP OAuth authentication disabled. The /api/mcp endpoint now accepts only gateway Hoop bearer tokens."))
	},
}

var mcpAuthStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current MCP OAuth authentication configuration",
	Run: func(cmd *cobra.Command, args []string) {
		current := mcpAuthGetOrEmpty()
		mcpAuthPrintStatus(current)
	},
}

var mcpAuthConfigureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Update MCP OAuth configuration without changing enabled state",
	Long: `Update the MCP OAuth configuration without toggling enable/disable.
Use --resource-uri to change the canonical RFC 8707 audience binding.
Use --groups-claim to change the JWT claim from which user groups are extracted.
Use --client-id/--client-secret to set a statically pre-registered OAuth client
for IdPs without RFC 7591 Dynamic Client Registration support (JumpCloud, Okta,
Entra ID). Prefer a public client (no secret) with PKCE: the registration shim
discloses the secret to any registering MCP client.`,
	Run: func(cmd *cobra.Command, args []string) {
		if mcpAuthResourceURIFlag == "" && mcpAuthGroupsClaimFlag == "" &&
			mcpAuthClientIDFlag == "" && mcpAuthClientSecretFlag == "" {
			styles.PrintErrorAndExit("nothing to configure: provide --resource-uri, --groups-claim, --client-id and/or --client-secret")
		}
		current := mcpAuthGetOrEmpty()
		if mcpAuthResourceURIFlag != "" {
			current["resource_uri"] = mcpAuthResourceURIFlag
		}
		if mcpAuthGroupsClaimFlag != "" {
			current["groups_claim"] = mcpAuthGroupsClaimFlag
		}
		if mcpAuthClientIDFlag != "" {
			current["client_id"] = mcpAuthClientIDFlag
		}
		if mcpAuthClientSecretFlag != "" {
			current["client_secret"] = mcpAuthClientSecretFlag
		}
		mcpAuthPutOrDie(current)
		fmt.Println(styles.Fainted("MCP OAuth configuration updated."))
		mcpAuthPrintStatus(current)
	},
}

func mcpAuthGetOrEmpty() map[string]any {
	conf := clientconfig.GetClientConfigOrDie()
	obj, _, err := httpRequest(&apiResource{
		suffixEndpoint: mcpAuthEndpoint,
		conf:           conf,
		decodeTo:       "object",
	})
	if err != nil {
		styles.PrintErrorAndExit("failed loading current MCP auth config: %v", err)
	}
	resp, _ := obj.(map[string]any)
	if resp == nil {
		resp = map[string]any{}
	}
	return resp
}

func mcpAuthPutOrDie(payload map[string]any) {
	conf := clientconfig.GetClientConfigOrDie()
	body := map[string]any{
		"enabled":       mcpAuthBool(payload["enabled"]),
		"resource_uri":  mcpAuthString(payload["resource_uri"]),
		"groups_claim":  mcpAuthString(payload["groups_claim"]),
		"client_id":     mcpAuthString(payload["client_id"]),
		"client_secret": mcpAuthString(payload["client_secret"]),
	}
	if _, err := httpBodyRequest(&apiResource{
		suffixEndpoint: mcpAuthEndpoint,
		conf:           conf,
		decodeTo:       "object",
	}, "PUT", body); err != nil {
		styles.PrintErrorAndExit("failed updating MCP auth config: %v", err)
	}
}

func mcpAuthPrintStatus(cfg map[string]any) {
	enabled := mcpAuthBool(cfg["enabled"])
	resourceURI := mcpAuthString(cfg["resource_uri"])
	if resourceURI == "" {
		resourceURI = "(default: <api-url>/api/mcp)"
	}
	groupsClaim := mcpAuthString(cfg["groups_claim"])
	if groupsClaim == "" {
		groupsClaim = "(default: groups)"
	}
	clientID := mcpAuthString(cfg["client_id"])
	staticClient := clientID
	if clientID == "" {
		staticClient = "(none: Dynamic Client Registration via IdP)"
	} else if mcpAuthString(cfg["client_secret"]) != "" {
		staticClient += " (confidential)"
	} else {
		staticClient += " (public, PKCE)"
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf(`MCP OAuth Status:
  State:         %s
  Resource URI:  %s
  Groups Claim:  %s
  Static Client: %s
`, state, resourceURI, groupsClaim, staticClient)
}

func mcpAuthBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func mcpAuthString(v any) string {
	s, _ := v.(string)
	return s
}
