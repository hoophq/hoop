package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
)

var rolesFlags struct {
	resource    string
	jsonOutput  bool
	quietOutput bool
}

var rolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "List roles",
	Long:  "List all roles (connections associated with resources), optionally filtered by resource.",
	Example: `  # List all roles
  hoop roles

  # Filter by resource
  hoop roles --resource my-db

  # Output as JSON
  hoop roles --json`,
	Run: func(cmd *cobra.Command, args []string) {
		runRoles()
	},
}

func init() {
	f := rolesCmd.Flags()
	f.StringVar(&rolesFlags.resource, "resource", "", "Filter by resource name")
	f.BoolVar(&rolesFlags.jsonOutput, "json", false, "Output as formatted JSON")
	f.BoolVar(&rolesFlags.quietOutput, "quiet", false, "Output as compact JSON (for scripting)")
	rootCmd.AddCommand(rolesCmd)
}

func runRoles() {
	config := clientconfig.GetClientConfigOrDie()

	params := url.Values{}
	if rolesFlags.resource != "" {
		params.Set("resource_name", rolesFlags.resource)
	}

	rawURL := config.ApiURL + "/api/connections"
	if len(params) > 0 {
		rawURL += "?" + params.Encode()
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to fetch roles: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("roles http response %v", httpResp.StatusCode)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != 200 {
		styles.PrintErrorAndExit("Failed to fetch roles, status=%v, body=%v", httpResp.StatusCode, string(body))
	}

	var roles []map[string]any
	if err := json.Unmarshal(body, &roles); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	// Keep only roles tied to a resource when no explicit filter is set.
	if rolesFlags.resource == "" {
		var filtered []map[string]any
		for _, r := range roles {
			if rn := toStr(r["resource_name"]); rn != "-" && rn != "" {
				filtered = append(filtered, r)
			}
		}
		roles = filtered
	}

	if rolesFlags.quietOutput {
		out, _ := json.Marshal(roles)
		fmt.Println(string(out))
		return
	}
	if rolesFlags.jsonOutput {
		out, _ := json.MarshalIndent(roles, "", "  ")
		fmt.Println(string(out))
		return
	}

	displayRoles(roles)
}

func displayRoles(roles []map[string]any) {
	if len(roles) == 0 {
		fmt.Println("No roles found")
		return
	}

	var rows [][]string
	for _, r := range roles {
		rows = append(rows, []string{
			toStr(r["name"]),
			toStr(r["resource_name"]),
			toStr(r["type"]),
			toStr(r["subtype"]),
			toStr(r["status"]),
		})
	}
	fmt.Println(styles.RenderTable([]string{"NAME", "RESOURCE", "TYPE", "SUBTYPE", "STATUS"}, rows))
}
