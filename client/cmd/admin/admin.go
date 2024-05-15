package admin

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/version"
	"github.com/spf13/cobra"
)

var hoopVersionStr = version.Get().Version

func init() {
	MainCmd.AddCommand(deleteCmd)
	MainCmd.AddCommand(getCmd)
	MainCmd.AddCommand(createCmd)
	MainCmd.AddCommand(serverInfoCmd)
	MainCmd.AddCommand(openWebhooksDashboardCmd)

	serverInfoCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One off: (json)")
}

var MainCmd = &cobra.Command{
	Use:   "admin",
	Short: "Experimental admin commands",
}

var serverInfoOutput = `Tenant Type:    %v
Grpc URL:       %v
Version:        %v
Gateway Commit: %v
Webapp Commit:  %v

Configuration:
  Log Level:               %v
  Go Debug:                %v
  Admin Username:          %v
  Redact Credentials:      %v
  Webhook App Credentials: %v
  Ask AI Credentials:      %v
  IDP Audience:            %v
  IDP Custom Scopes:       %v
  Postgrest Role:          %v
`

var serverInfoCmd = &cobra.Command{
	Use:     "serverinfo",
	Aliases: []string{"server-info"},
	Short:   "Get information about the gateway",
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		decodeTo := "object"
		if outputFlag == "json" {
			decodeTo = "raw"
		}
		obj, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/serverinfo", conf: conf, decodeTo: decodeTo})
		if err != nil {
			out := styles.ClientErrorSimple(fmt.Sprintf("failed obtaining server info response, reason=%v", err))
			fmt.Println(out)
			return
		}
		if outputFlag == "json" {
			rawData, _ := obj.([]byte)
			fmt.Println(string(rawData))
			return
		}
		displayFn := func(val any) string {
			isSet, ok := val.(bool)
			if ok && isSet {
				return "set"
			}
			return "not set"
		}
		if resp, _ := obj.(map[string]any); len(resp) > 0 {
			fmt.Printf(serverInfoOutput,
				resp["tenancy_type"],
				resp["grpc_url"],
				resp["version"],
				resp["gateway_commit"],
				resp["webapp_commit"],
				resp["log_level"],
				resp["go_debug"],
				resp["admin_username"],
				displayFn(resp["has_redact_credentials"]),
				displayFn(resp["has_webhook_app_key"]),
				displayFn(resp["has_ask_ai_credentials"]),
				displayFn(resp["has_idp_audience"]),
				displayFn(resp["has_idp_custom_scopes"]),
				displayFn(resp["has_postgrest_role"]),
			)
			return
		}
		fmt.Println("Empty response")
	},
}

var openWebhooksDashboardCmd = &cobra.Command{
	Use:     "webhooks-dashboard",
	Short:   "Open the webhooks app dashboard",
	Aliases: []string{"webhook-dashboard"},
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		obj, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/webhooks-dashboard", conf: conf, decodeTo: "object"})
		if err != nil {
			out := styles.ClientErrorSimple(fmt.Sprintf("failed open webhooks dashboard, error=%v", err))
			fmt.Println(out)
			return
		}
		resp := obj.(map[string]any)
		if v, ok := resp["url"]; ok {
			dashboardURL := fmt.Sprintf("%v", v)
			if err := openBrowser(fmt.Sprintf("%v", dashboardURL)); err != nil {
				fmt.Printf("Failed to open browser.\nClick on the link below:\n\n%s\n\n", dashboardURL)
			}
		}
	},
}

func openBrowser(url string) (err error) {
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	return
}
