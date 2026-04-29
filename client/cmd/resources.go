package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
)

// ---- flags ----

var resourcesListFlags struct {
	search      string
	name        string
	subtype     string
	jsonOutput  bool
	quietOutput bool
}

var resourcesCreateFlags struct {
	resType     string
	subtype     string
	agentID     string
	envVars     []string
	jsonOutput  bool
	quietOutput bool
}

var resourcesRolesListFlags struct {
	jsonOutput  bool
	quietOutput bool
}

var resourcesRolesCreateFlags struct {
	resource    string
	roleType    string
	subtype     string
	agentID     string
	command     []string
	secrets     []string
	jsonOutput  bool
	quietOutput bool
}

// ---- commands ----

var resourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "Manage resources",
	Long:  "Create and list resources and their associated roles.",
}

var resourcesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List resources",
	Long:  "List resources you have access to, with optional filters.",
	Example: `  # List all resources
  hoop resources list

  # Filter by name
  hoop resources list --name my-db

  # Search across name, type, and subtype
  hoop resources list --search postgres

  # Output as JSON
  hoop resources list --json`,
	Run: func(cmd *cobra.Command, args []string) {
		runResourcesList()
	},
}

var resourcesCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a resource",
	Long:  "Create a new resource. The name is passed as a positional argument.",
	Args:  cobra.ExactArgs(1),
	Example: `  # Create a database resource
  hoop resources create my-db --type database --subtype postgres --agent-id <uuid>

  # With environment variables
  hoop resources create my-db --type database --agent-id <uuid> --env-var HOST=mydb.internal --env-var PORT=5432`,
	Run: func(cmd *cobra.Command, args []string) {
		runResourcesCreate(args[0])
	},
}

var resourcesRolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "Manage resource roles",
	Long:  "Create and list roles (connections) associated with resources.",
}

var resourcesRolesListCmd = &cobra.Command{
	Use:   "list <resource-name>",
	Short: "List roles for a resource",
	Long:  "List all roles (connections) associated with a given resource.",
	Args:  cobra.ExactArgs(1),
	Example: `  hoop resources roles list my-db
  hoop resources roles list my-db --json`,
	Run: func(cmd *cobra.Command, args []string) {
		runResourcesRolesList(args[0])
	},
}

var resourcesRolesCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a role for a resource",
	Long:  "Create a new role (connection) and associate it with a resource. The role name is passed as a positional argument.",
	Args:  cobra.ExactArgs(1),
	Example: `  # Create a postgres role for a resource
  hoop resources roles create pgdemo --resource my-db --type database --subtype postgres --agent-id <uuid>

  # With secrets
  hoop resources roles create pgdemo --resource my-db --type database --subtype postgres --agent-id <uuid> \
    --secret "envvar:DB_HOST=bXlkYi5pbnRlcm5hbA=="`,
	Run: func(cmd *cobra.Command, args []string) {
		runResourcesRolesCreate(args[0])
	},
}

func init() {
	// resources list
	fl := resourcesListCmd.Flags()
	fl.StringVar(&resourcesListFlags.search, "search", "", "Search by name, type, or subtype")
	fl.StringVar(&resourcesListFlags.name, "name", "", "Filter by resource name")
	fl.StringVar(&resourcesListFlags.subtype, "subtype", "", "Filter by subtype")
	fl.BoolVar(&resourcesListFlags.jsonOutput, "json", false, "Output as formatted JSON")
	fl.BoolVar(&resourcesListFlags.quietOutput, "quiet", false, "Output as compact JSON (for scripting)")

	// resources create
	fc := resourcesCreateCmd.Flags()
	fc.StringVar(&resourcesCreateFlags.resType, "type", "", "Resource type: database, application, custom (required)")
	fc.StringVar(&resourcesCreateFlags.subtype, "subtype", "", "Resource subtype (e.g. postgres, mysql, tcp)")
	fc.StringVar(&resourcesCreateFlags.agentID, "agent-id", "", "Agent UUID")
	fc.StringArrayVar(&resourcesCreateFlags.envVars, "env-var", nil, "Environment variable as KEY=VALUE (repeatable)")
	fc.BoolVar(&resourcesCreateFlags.jsonOutput, "json", false, "Output created resource as formatted JSON")
	fc.BoolVar(&resourcesCreateFlags.quietOutput, "quiet", false, "Output created resource as compact JSON")
	_ = resourcesCreateCmd.MarkFlagRequired("type")

	// resources roles list
	frl := resourcesRolesListCmd.Flags()
	frl.BoolVar(&resourcesRolesListFlags.jsonOutput, "json", false, "Output as formatted JSON")
	frl.BoolVar(&resourcesRolesListFlags.quietOutput, "quiet", false, "Output as compact JSON (for scripting)")

	// resources roles create
	frc := resourcesRolesCreateCmd.Flags()
	frc.StringVar(&resourcesRolesCreateFlags.resource, "resource", "", "Resource name to associate with (required)")
	frc.StringVar(&resourcesRolesCreateFlags.roleType, "type", "", "Role type: database, application, custom (required)")
	frc.StringVar(&resourcesRolesCreateFlags.subtype, "subtype", "", "Role subtype (e.g. postgres, mysql, tcp)")
	frc.StringVar(&resourcesRolesCreateFlags.agentID, "agent-id", "", "Agent UUID (required)")
	frc.StringArrayVar(&resourcesRolesCreateFlags.command, "command", nil, "Command argument (repeatable, e.g. --command /bin/bash)")
	frc.StringArrayVar(&resourcesRolesCreateFlags.secrets, "secret", nil, `Secret as KEY=VALUE (repeatable, e.g. --secret "envvar:HOST=base64val")`)
	frc.BoolVar(&resourcesRolesCreateFlags.jsonOutput, "json", false, "Output created role as formatted JSON")
	frc.BoolVar(&resourcesRolesCreateFlags.quietOutput, "quiet", false, "Output created role as compact JSON")
	_ = resourcesRolesCreateCmd.MarkFlagRequired("resource")
	_ = resourcesRolesCreateCmd.MarkFlagRequired("type")
	_ = resourcesRolesCreateCmd.MarkFlagRequired("agent-id")

	resourcesRolesCmd.AddCommand(resourcesRolesListCmd)
	resourcesRolesCmd.AddCommand(resourcesRolesCreateCmd)
	resourcesCmd.AddCommand(resourcesListCmd)
	resourcesCmd.AddCommand(resourcesCreateCmd)
	resourcesCmd.AddCommand(resourcesRolesCmd)
	rootCmd.AddCommand(resourcesCmd)
}

// ---- resources list ----

func runResourcesList() {
	config := clientconfig.GetClientConfigOrDie()

	params := url.Values{}
	if resourcesListFlags.search != "" {
		params.Set("search", resourcesListFlags.search)
	}
	if resourcesListFlags.name != "" {
		params.Set("name", resourcesListFlags.name)
	}
	if resourcesListFlags.subtype != "" {
		params.Set("subtype", resourcesListFlags.subtype)
	}

	rawURL := config.ApiURL + "/api/resources"
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
		styles.PrintErrorAndExit("Failed to fetch resources: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources list http response %v", httpResp.StatusCode)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != 200 {
		styles.PrintErrorAndExit("Failed to fetch resources, status=%v, body=%v", httpResp.StatusCode, string(body))
	}

	var resources []map[string]any
	if err := json.Unmarshal(body, &resources); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	if resourcesListFlags.quietOutput {
		fmt.Println(string(body))
		return
	}
	if resourcesListFlags.jsonOutput {
		out, _ := json.MarshalIndent(resources, "", "  ")
		fmt.Println(string(out))
		return
	}

	displayResourcesList(resources)
}

func displayResourcesList(resources []map[string]any) {
	if len(resources) == 0 {
		fmt.Println("No resources found")
		return
	}

	var rows [][]string
	for _, r := range resources {
		rows = append(rows, []string{
			toStr(r["name"]),
			toStr(r["type"]),
			toStr(r["subtype"]),
			toStr(r["agent_id"]),
		})
	}
	fmt.Println(styles.RenderTable([]string{"NAME", "TYPE", "SUBTYPE", "AGENT ID"}, rows))
}

// ---- resources create ----

func runResourcesCreate(name string) {
	config := clientconfig.GetClientConfigOrDie()

	envVars := parseKeyValuePairs(resourcesCreateFlags.envVars)

	payload := map[string]any{
		"name":     name,
		"type":     resourcesCreateFlags.resType,
		"env_vars": envVars,
	}
	if resourcesCreateFlags.subtype != "" {
		payload["subtype"] = resourcesCreateFlags.subtype
	}
	if resourcesCreateFlags.agentID != "" {
		payload["agent_id"] = resourcesCreateFlags.agentID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}

	req, err := http.NewRequest("POST", config.ApiURL+"/api/resources", bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to create resource: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources create http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != 201 {
		styles.PrintErrorAndExit("Failed to create resource, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var resource map[string]any
	if err := json.Unmarshal(respBody, &resource); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	if resourcesCreateFlags.quietOutput {
		fmt.Println(string(respBody))
		return
	}

	out, _ := json.MarshalIndent(resource, "", "  ")
	fmt.Println(string(out))
}

// ---- resources roles list ----

func runResourcesRolesList(resourceName string) {
	config := clientconfig.GetClientConfigOrDie()

	params := url.Values{}
	params.Set("resource_name", resourceName)

	rawURL := config.ApiURL + "/api/connections?" + params.Encode()

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

	log.Debugf("resources roles list http response %v", httpResp.StatusCode)

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

	if resourcesRolesListFlags.quietOutput {
		fmt.Println(string(body))
		return
	}
	if resourcesRolesListFlags.jsonOutput {
		out, _ := json.MarshalIndent(roles, "", "  ")
		fmt.Println(string(out))
		return
	}

	displayRolesList(roles)
}

func displayRolesList(roles []map[string]any) {
	if len(roles) == 0 {
		fmt.Println("No roles found")
		return
	}

	var rows [][]string
	for _, r := range roles {
		rows = append(rows, []string{
			toStr(r["name"]),
			toStr(r["type"]),
			toStr(r["subtype"]),
			toStr(r["status"]),
		})
	}
	fmt.Println(styles.RenderTable([]string{"NAME", "TYPE", "SUBTYPE", "STATUS"}, rows))
}

// ---- resources roles create ----

func runResourcesRolesCreate(name string) {
	config := clientconfig.GetClientConfigOrDie()

	secrets := parseKeyValuePairsAny(resourcesRolesCreateFlags.secrets)

	payload := map[string]any{
		"name":          name,
		"resource_name": resourcesRolesCreateFlags.resource,
		"type":          resourcesRolesCreateFlags.roleType,
		"agent_id":      resourcesRolesCreateFlags.agentID,
		"secret":        secrets,
	}
	if resourcesRolesCreateFlags.subtype != "" {
		payload["subtype"] = resourcesRolesCreateFlags.subtype
	}
	if len(resourcesRolesCreateFlags.command) > 0 {
		payload["command"] = resourcesRolesCreateFlags.command
	}

	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}

	req, err := http.NewRequest("POST", config.ApiURL+"/api/connections", bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to create role: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources roles create http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != 201 {
		styles.PrintErrorAndExit("Failed to create role, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var role map[string]any
	if err := json.Unmarshal(respBody, &role); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	if resourcesRolesCreateFlags.quietOutput {
		fmt.Println(string(respBody))
		return
	}

	out, _ := json.MarshalIndent(role, "", "  ")
	fmt.Println(string(out))
}

// ---- helpers ----

// parseKeyValuePairs splits "KEY=VALUE" strings into a map[string]string.
func parseKeyValuePairs(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// parseKeyValuePairsAny splits "KEY=VALUE" strings into a map[string]any.
func parseKeyValuePairsAny(pairs []string) map[string]any {
	m := make(map[string]any, len(pairs))
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		if k != "" {
			m[k] = v
		}
	}
	return m
}
