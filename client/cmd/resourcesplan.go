package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ---- types ----

type planFileItem struct {
	ResourceName   string   `yaml:"resource_name" json:"resource_name"`
	Type           string   `yaml:"type"          json:"type"`
	Role           string   `yaml:"role"          json:"role"`
	SourceRole     string   `yaml:"source_role"   json:"source_role"`
	Scopes         []string `yaml:"scopes"        json:"scopes"`
	Privileges     []string `yaml:"privileges"    json:"privileges"`
	RotatePassword bool     `yaml:"rotate_password,omitempty" json:"rotate_password,omitempty"`
}

type planFile struct {
	Items []planFileItem `yaml:"items" json:"items"`
}

type planResultItem struct {
	SID          string `yaml:"sid"           json:"sid"`
	ResourceName string `yaml:"resource_name" json:"resource_name"`
	Role         string `yaml:"role"          json:"role"`
	SourceRole   string `yaml:"source_role"   json:"source_role"`
	Status       string `yaml:"status"        json:"status"`
	Message      string `yaml:"message"       json:"message"`
}

type planResultFile struct {
	Results []planResultItem `yaml:"results" json:"results"`
}

// ---- flags ----

var resourcesPlanFlags struct {
	role           string
	scopes         []string
	privileges     []string
	resType        string
	rotatePassword bool
	inputFile      string
	outputFile     string
	jsonOutput     bool
	quietOutput    bool
}

var resourcesApplyFlags struct {
	sid         string
	inputFile   string
	jsonOutput  bool
	quietOutput bool
}

// ---- commands ----

var resourcesPlanCmd = &cobra.Command{
	Use:   "plan [resource-name]",
	Short: "Plan resource provisioning",
	Long:  "Validates provisioning plans for one or more resources. Outputs results to stdout and optionally writes them to a file for use with 'apply'.",
	Example: `  # Single resource
  hoop resources plan my-postgres --role ro --scopes mydb --privileges SELECT

  # Multiple scopes and privileges
  hoop resources plan my-postgres --role ro \
    --scopes mydb --scopes otherdb \
    --privileges SELECT --privileges INSERT

  # Batch via file
  hoop resources plan -f plan.yaml

  # Save plan results for later apply
  hoop resources plan -f plan.yaml -o plan-result.yaml`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var resourceName string
		if len(args) == 1 {
			resourceName = args[0]
		}
		runResourcesPlan(resourceName)
	},
}

var resourcesApplyCmd = &cobra.Command{
	Use:   "apply [resource-name]",
	Short: "Apply a resource provisioning plan",
	Long:  "Applies a previously created provisioning plan. Accepts a plan result file (from 'plan -o') or a resource name with --sid.",
	Example: `  # Apply using plan result file
  hoop resources apply -f plan-result.yaml

  # Apply a single plan by SID
  hoop resources apply my-postgres --sid 5701046A-7B7A-4A78-ABB0-A24C95E6FE54`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var resourceName string
		if len(args) == 1 {
			resourceName = args[0]
		}
		runResourcesApply(resourceName)
	},
}

var resourcesPlanStatusCmd = &cobra.Command{
	Use:     "status <sid>",
	Short:   "Show the output of a plan session",
	Long:    "Fetches the session identified by the given SID and prints its output contents.",
	Args:    cobra.ExactArgs(1),
	Example: `  hoop resources plan status 5701046A-7B7A-4A78-ABB0-A24C95E6FE54`,
	Run: func(cmd *cobra.Command, args []string) {
		config := clientconfig.GetClientConfigOrDie()
		output, err := fetchSessionOutput(config, args[0])
		if err != nil {
			styles.PrintErrorAndExit("Failed to fetch session output: %v", err)
		}
		fmt.Print(output)
	},
}

func init() {
	// resources plan flags
	fp := resourcesPlanCmd.Flags()
	fp.StringVarP(&resourcesPlanFlags.inputFile, "file", "f", "", "Path to a YAML plan file for batch operations")
	fp.StringVarP(&resourcesPlanFlags.outputFile, "output", "o", "", "Write plan results as YAML to this file (usable as input for 'apply -f')")
	fp.StringVar(&resourcesPlanFlags.role, "role", "", "Role name, e.g. ro or rw (required without -f)")
	fp.StringSliceVar(&resourcesPlanFlags.scopes, "scopes", nil, "Database/schema scope, e.g. mydb or mydb.public (comma-separated or repeatable, required without -f)")
	fp.StringSliceVar(&resourcesPlanFlags.privileges, "privileges", nil, "Privilege to grant, e.g. SELECT,INSERT (comma-separated or repeatable, required without -f)")
	fp.StringVar(&resourcesPlanFlags.resType, "type", "managed", "Management type: managed or external")
	fp.BoolVar(&resourcesPlanFlags.rotatePassword, "rotate-password", false, "Force password rotation")
	fp.BoolVar(&resourcesPlanFlags.jsonOutput, "json", false, "Output results as formatted JSON")
	fp.BoolVar(&resourcesPlanFlags.quietOutput, "quiet", false, "Output results as compact JSON")

	// resources apply flags
	fa := resourcesApplyCmd.Flags()
	fa.StringVarP(&resourcesApplyFlags.inputFile, "file", "f", "", "Path to a plan result YAML file (output of 'plan -o')")
	fa.StringVar(&resourcesApplyFlags.sid, "sid", "", "Plan SID returned by 'plan' (required without -f)")
	fa.BoolVar(&resourcesApplyFlags.jsonOutput, "json", false, "Output results as formatted JSON")
	fa.BoolVar(&resourcesApplyFlags.quietOutput, "quiet", false, "Output results as compact JSON")

	resourcesPlanCmd.AddCommand(resourcesPlanStatusCmd)
	resourcesCmd.AddCommand(resourcesPlanCmd)
	resourcesCmd.AddCommand(resourcesApplyCmd)
}

// ---- plan ----

func runResourcesPlan(resourceName string) {
	config := clientconfig.GetClientConfigOrDie()

	if resourcesPlanFlags.inputFile != "" {
		runResourcesPlanBatch(config)
		return
	}

	if resourceName == "" {
		styles.PrintErrorAndExit("resource-name is required when not using -f")
	}
	if resourcesPlanFlags.role == "" {
		styles.PrintErrorAndExit("--role is required when not using -f")
	}
	if len(resourcesPlanFlags.scopes) == 0 {
		styles.PrintErrorAndExit("--scopes is required when not using -f")
	}
	if len(resourcesPlanFlags.privileges) == 0 {
		styles.PrintErrorAndExit("--privileges is required when not using -f")
	}

	payload := map[string]any{
		"type":            resourcesPlanFlags.resType,
		"role":            resourcesPlanFlags.role,
		"scopes":          resourcesPlanFlags.scopes,
		"privileges":      resourcesPlanFlags.privileges,
		"rotate_password": resourcesPlanFlags.rotatePassword,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}
	log.Debugf("resource plan request: %v", string(body))

	url := fmt.Sprintf("%s/api/resources/%s/plan", config.ApiURL, resourceName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to call plan endpoint: %v", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		styles.PrintErrorAndExit("Plan failed, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var res planResultItem
	if err := json.Unmarshal(respBody, &res); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	log.Debugf("resources plan response, status_code=%v, sid=%v, resource=%v, role=%v, source_role=%v, status=%v, message=%v",
		httpResp.StatusCode, res.SID, res.ResourceName, res.Role, res.SourceRole, res.Status, res.Message)

	results := planResultFile{Results: []planResultItem{res}}
	outputPlanResults(config, results, respBody, false)
}

func runResourcesPlanBatch(config *clientconfig.Config) {
	data, err := os.ReadFile(resourcesPlanFlags.inputFile)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read plan file: %v", err)
	}

	var pf planFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		styles.PrintErrorAndExit("Failed to parse plan file: %v", err)
	}
	if len(pf.Items) == 0 {
		styles.PrintErrorAndExit("Plan file contains no items")
	}

	payload := map[string]any{"items": pf.Items}
	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}
	log.Debugf("resource plan batch request: %v", string(body))

	req, err := http.NewRequest("POST", config.ApiURL+"/api/resources/plan", bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to call batch plan endpoint: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources plan batch http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		styles.PrintErrorAndExit("Batch plan failed, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var results planResultFile
	if err := json.Unmarshal(respBody, &results); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	outputPlanResults(config, results, respBody, true)
}

func outputPlanResults(config *clientconfig.Config, results planResultFile, rawBody []byte, isBatch bool) {
	if resourcesPlanFlags.quietOutput {
		fmt.Println(string(rawBody))
		writePlanOutputFile(results)
		return
	}

	if resourcesPlanFlags.jsonOutput {
		out, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(out))
		writePlanOutputFile(results)
		return
	}

	if isBatch {
		displayPlanResultsTable(results.Results)
	} else {
		displayPlanResults(config, results.Results)
	}
	writePlanOutputFile(results)
}

func writePlanOutputFile(results planResultFile) {
	if resourcesPlanFlags.outputFile == "" {
		return
	}
	data, err := yaml.Marshal(results)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode plan results: %v", err)
	}
	if err := os.WriteFile(resourcesPlanFlags.outputFile, data, 0644); err != nil {
		styles.PrintErrorAndExit("Failed to write plan output file: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Plan results written to %s\n", resourcesPlanFlags.outputFile)
}

func displayPlanResultsTable(results []planResultItem) {
	if len(results) == 0 {
		fmt.Println("No plan results")
		return
	}
	var rows [][]string
	for _, r := range results {
		rows = append(rows, []string{r.SID, r.ResourceName, r.Role, r.Status, toStr(r.Message)})
	}
	fmt.Println(styles.RenderTable([]string{"SID", "RESOURCE", "ROLE", "STATUS", "MESSAGE"}, rows))
}

func displayPlanResults(config *clientconfig.Config, results []planResultItem) {
	if len(results) == 0 {
		fmt.Println("No plan results")
		return
	}
	for _, r := range results {
		output, err := fetchSessionOutput(config, r.SID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch session output for %s: %v\n", r.ResourceName, err)
		} else {
			fmt.Print(output)
		}
	}
}

func fetchSessionOutput(config *clientconfig.Config, sid string) (string, error) {
	url := fmt.Sprintf("%s/api/sessions/%s?expand=event_stream&event_stream=utf8", config.ApiURL, sid)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed creating request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed performing request: %v", err)
	}
	defer resp.Body.Close()

	log.Debugf("fetch session output http response %v", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status=%v, body=%v", resp.StatusCode, string(body))
	}

	var session map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("failed decoding response: %v", err)
	}

	eventStream, ok := session["event_stream"].([]any)
	if !ok || len(eventStream) == 0 {
		return "", nil
	}
	output, _ := eventStream[0].(string)
	return output, nil
}

// ---- apply ----

func runResourcesApply(resourceName string) {
	config := clientconfig.GetClientConfigOrDie()

	if resourcesApplyFlags.inputFile != "" {
		runResourcesApplyBatch(config)
		return
	}

	if resourceName == "" {
		styles.PrintErrorAndExit("resource-name is required when not using -f")
	}
	if resourcesApplyFlags.sid == "" {
		styles.PrintErrorAndExit("--sid is required when not using -f")
	}

	payload := map[string]any{"sid": resourcesApplyFlags.sid}
	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}

	log.Debugf("resource apply request: %v", string(body))

	url := fmt.Sprintf("%s/api/resources/%s/apply", config.ApiURL, resourceName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to call apply endpoint: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources apply http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		styles.PrintErrorAndExit("Apply failed, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var result planResultItem
	if err := json.Unmarshal(respBody, &result); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}
	if result.ResourceName == "" {
		result.ResourceName = resourceName
	}

	outputApplyResults(config, []planResultItem{result}, respBody, false)
}

func runResourcesApplyBatch(config *clientconfig.Config) {
	data, err := os.ReadFile(resourcesApplyFlags.inputFile)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read plan result file: %v", err)
	}

	var pf planResultFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		styles.PrintErrorAndExit("Failed to parse plan result file: %v", err)
	}
	if len(pf.Results) == 0 {
		styles.PrintErrorAndExit("Plan result file contains no results")
	}

	type applyItem struct {
		SID          string `json:"sid"`
		ResourceName string `json:"resource_name"`
	}
	var items []applyItem
	for _, r := range pf.Results {
		if r.Status == "failed" {
			log.Warnf("skipping resource %q: plan status is %q", r.ResourceName, r.Status)
			continue
		}
		items = append(items, applyItem{SID: r.SID, ResourceName: r.ResourceName})
	}
	if len(items) == 0 {
		styles.PrintErrorAndExit("No applicable plan results to apply")
	}

	payload := map[string]any{"items": items}
	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}
	log.Debugf("resource apply batch request: %v", string(body))

	req, err := http.NewRequest("POST", config.ApiURL+"/api/resources/apply", bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to call batch apply endpoint: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("resources apply batch http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		styles.PrintErrorAndExit("Batch apply failed, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var applyResp struct {
		Results []planResultItem `json:"results"`
	}
	if err := json.Unmarshal(respBody, &applyResp); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	outputApplyResults(config, applyResp.Results, respBody, true)
}

func outputApplyResults(config *clientconfig.Config, results []planResultItem, rawBody []byte, isBatch bool) {
	if resourcesApplyFlags.quietOutput {
		fmt.Println(string(rawBody))
		return
	}
	if resourcesApplyFlags.jsonOutput {
		out, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(out))
		return
	}
	if isBatch {
		displayApplyResultsTable(results)
	} else {
		displayApplyResults(config, results)
	}
}

func displayApplyResultsTable(results []planResultItem) {
	if len(results) == 0 {
		fmt.Println("No apply results")
		return
	}
	var rows [][]string
	for _, r := range results {
		rows = append(rows, []string{r.SID, r.ResourceName, r.Status, toStr(r.Message)})
	}
	fmt.Println(styles.RenderTable([]string{"SID", "RESOURCE", "STATUS", "MESSAGE"}, rows))
}

func displayApplyResults(config *clientconfig.Config, results []planResultItem) {
	if len(results) == 0 {
		fmt.Println("No apply results")
		return
	}
	for _, r := range results {
		output, err := fetchSessionOutput(config, r.SID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch session output for %s: %v\n", r.ResourceName, err)
		} else {
			fmt.Print(output)
		}
	}
}
