package admin

import (
	"bytes"
	"fmt"
	"net/http"
	"os"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/version"
	"github.com/spf13/cobra"
)

var (
	hoopVersionStr = version.Get().Version
	expressApiFlag bool
)

func init() {
	MainCmd.AddCommand(deleteCmd)
	MainCmd.AddCommand(getCmd)
	MainCmd.AddCommand(createCmd)
	MainCmd.AddCommand(gatewayInfoCmd)
	MainCmd.AddCommand(targetToConnection)
	MainCmd.PersistentFlags().BoolVar(&expressApiFlag, "apiv2", false, "perform request in the new api")
	_ = MainCmd.PersistentFlags().MarkHidden("apiv2")

	if os.Getenv("HOOP_APIV2") == "true" {
		expressApiFlag = true
	}
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
