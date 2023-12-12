package admin

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/runopsio/hoop/client/cmd/admin/migrate"
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
	MainCmd.AddCommand(gatewayInfoCmd)
	MainCmd.AddCommand(openWebhooksDashboardCmd)
	MainCmd.AddCommand(migrate.MainCmd)
}

var MainCmd = &cobra.Command{
	Use:   "admin",
	Short: "Experimental admin commands",
}

var gatewayInfoCmd = &cobra.Command{
	Use:   "gateway-info",
	Short: "Get information about the gateway",
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		_, headers, err := httpRequest(&apiResource{suffixEndpoint: "/api/healthz", conf: conf})
		if err != nil {
			out := styles.ClientErrorSimple(fmt.Sprintf("API at %v, GET /api/healthz responded with error=%v", conf.ApiURL, err))
			fmt.Println(out)
		} else {
			fmt.Printf("API is running at %v, GET /api/healthz %v\n", conf.ApiURL, headers[http.CanonicalHeaderKey("server")])
		}

		data, _, err := httpRequest(&apiResource{suffixEndpoint: "/js/manifest.edn", conf: conf})
		if err != nil {
			out := styles.ClientErrorSimple(fmt.Sprintf("Webapp is running at %v, responded with error=%v", conf.ApiURL, err))
			fmt.Println(out)
			return
		}
		ok := bytes.Contains(data.([]byte), []byte(`:module-id :app, :name :app, :output-name "app.js"`))
		if ok {
			fmt.Printf("Webapp is running at %v, GET /js/manifest.edn responded with success!\n", conf.ApiURL)
			return
		}
		out := styles.ClientErrorSimple(fmt.Sprintf("Webapp is running at %v, GET /js/manifest.edn did not render correctly!", conf.ApiURL))
		fmt.Println(out)
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
