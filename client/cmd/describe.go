package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var describeExampleDesc = `hoop describe db-read
hoop describe my-postgres-connection
hoop describe -o json redis-cache`

var describeCmd = &cobra.Command{
	Use:     "describe CONNECTION",
	Short:   "Show detailed information about a connection",
	Long:    "Display detailed information about a specific connection including command, plugins, and configuration",
	Example: describeExampleDesc,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runDescribe(args[0])
	},
}

func init() {
	describeCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One of: (json)")
	rootCmd.AddCommand(describeCmd)
}

func runDescribe(connectionName string) {
	config := clientconfig.GetClientConfigOrDie()

	connection, err := fetchConnection(config, connectionName)
	if err != nil {
		styles.PrintErrorAndExit("Failed to fetch connection '%s': %v", connectionName, err)
	}

	if outputFlag == "json" {
		jsonData, err := json.MarshalIndent(connection, "", "  ")
		if err != nil {
			styles.PrintErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Print(string(jsonData))
		return
	}

	displayConnectionDetails(config, connection)
}

func fetchConnection(config *clientconfig.Config, name string) (map[string]any, error) {
	url := fmt.Sprintf("%s/api/connections/%s", config.ApiURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %v", err)
	}
	defer resp.Body.Close()

	log.Debugf("http response %v", resp.StatusCode)

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("connection '%s' not found", name)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch connection, status=%v", resp.StatusCode)
	}

	var connection map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&connection); err != nil {
		return nil, fmt.Errorf("failed decoding response: %v", err)
	}

	return connection, nil
}

func displayConnectionDetails(config *clientconfig.Config, conn map[string]any) {
	name := toStr(conn["name"])
	connType := toStr(conn["type"])
	subType := toStr(conn["subtype"])
	status := toStr(conn["status"])
	agentID := toStr(conn["agent_id"])

	// Fetch agent information (use same approach as list)
	allAgentInfo := fetchAllAgentInfo(config)
	agentName := getAgentNameFromAllInfo(allAgentInfo, agentID)

	// Fetch plugins for this connection
	plugins := fetchConnectionPlugins(config, name)

	fmt.Printf("name: %s\n", name)

	if subType != "-" && subType != connType {
		fmt.Printf("type: %s (%s)\n", connType, subType)
	} else {
		fmt.Printf("type: %s\n", connType)
	}

	fmt.Printf("status: %s\n", strings.ToLower(status))
	fmt.Printf("agent: %s\n", agentName)

	// Full command
	cmdList, _ := conn["command"].([]any)
	command := joinFullCommand(cmdList)
	fmt.Printf("command: %s\n", command)

	// Plugins
	fmt.Printf("plugins: %s\n", formatPlugins(plugins))

	// Secrets
	secrets, _ := conn["secret"].(map[string]any)
	if secrets == nil {
		secrets, _ = conn["secrets"].(map[string]any)
	}
	if len(secrets) > 0 {
		fmt.Printf("secrets: %d configured\n", len(secrets))
	} else {
		fmt.Printf("secrets: -\n")
	}

	// Tags (if they exist)
	if tags := conn["connection_tags"]; tags != nil {
		if tagStr := formatTags(tags); tagStr != "-" {
			fmt.Printf("tags: %s\n", tagStr)
		}
	}

	// Environment variables (if they exist)
	if envs := conn["envs"]; envs != nil {
		if envMap, ok := envs.(map[string]any); ok && len(envMap) > 0 {
			fmt.Printf("environment: %d variables configured\n", len(envMap))
		}
	}
}

func fetchAllAgentInfo(config *clientconfig.Config) map[string]map[string]any {
	url := config.ApiURL + "/api/agents"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Debugf("failed creating agents request: %v", err)
		return nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		log.Debugf("failed performing agents request: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Debugf("failed to fetch agents, status=%v", resp.StatusCode)
		return nil
	}

	var agents []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		log.Debugf("failed decoding agents response: %v", err)
		return nil
	}

	// Create map of ID -> agent info
	agentMap := make(map[string]map[string]any)
	for _, agent := range agents {
		if id, ok := agent["id"].(string); ok {
			agentMap[id] = agent
		}
	}

	return agentMap
}

func getAgentNameFromAllInfo(agentInfo map[string]map[string]any, agentID string) string {
	if agentInfo == nil {
		return "-"
	}

	if agent, exists := agentInfo[agentID]; exists {
		if name := toStr(agent["name"]); name != "-" {
			return name
		}
	}

	return "-"
}

func fetchConnectionPlugins(config *clientconfig.Config, connectionName string) []string {
	url := fmt.Sprintf("%s/api/plugins", config.ApiURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Debugf("failed creating plugins request: %v", err)
		return []string{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		log.Debugf("failed performing plugins request: %v", err)
		return []string{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Debugf("failed to fetch plugins, status=%v", resp.StatusCode)
		return []string{}
	}

	var plugins []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		log.Debugf("failed decoding plugins response: %v", err)
		return []string{}
	}

	// Filter plugins that apply to this connection
	var connectionPlugins []string
	for _, plugin := range plugins {
		pluginName := toStr(plugin["name"])
		if pluginName == "-" {
			continue
		}

		connections, ok := plugin["connections"].([]any)
		if !ok {
			continue
		}

		for _, connObj := range connections {
			if connMap, ok := connObj.(map[string]any); ok {
				if toStr(connMap["name"]) == connectionName {
					connectionPlugins = append(connectionPlugins, pluginName)
					break
				}
			}
		}
	}

	sort.Strings(connectionPlugins)
	return connectionPlugins
}

func joinFullCommand(cmdList []any) string {
	if len(cmdList) == 0 {
		return "-"
	}

	var list []string
	for _, c := range cmdList {
		list = append(list, fmt.Sprintf("%q", c))
	}
	cmd := strings.Join(list, " ")

	// In describe we show the full command without truncating
	return fmt.Sprintf("[ %s ]", cmd)
}

func formatPlugins(plugins []string) string {
	if len(plugins) == 0 {
		return "-"
	}

	return strings.Join(plugins, ", ")
}

func formatTags(tags any) string {
	switch v := tags.(type) {
	case map[string]any:
		if len(v) == 0 {
			return "-"
		}
		var tagPairs []string
		for key, val := range v {
			formattedTag := formatSingleTag(key, toStr(val))
			if formattedTag != "" {
				tagPairs = append(tagPairs, formattedTag)
			}
		}
		if len(tagPairs) == 0 {
			return "-"
		}
		sort.Strings(tagPairs)
		return strings.Join(tagPairs, ", ")
	case []any:
		if len(v) == 0 {
			return "-"
		}
		var tagList []string
		for _, tag := range v {
			if tagStr := toStr(tag); tagStr != "-" {
				// For arrays, we assume they are already in key=value format
				formattedTag := formatTagString(tagStr)
				if formattedTag != "" {
					tagList = append(tagList, formattedTag)
				}
			}
		}
		if len(tagList) == 0 {
			return "-"
		}
		sort.Strings(tagList)
		return strings.Join(tagList, ", ")
	default:
		return "-"
	}
}

// formatSingleTag formats an individual tag based on separate key and value
func formatSingleTag(key, value string) string {
	if value == "-" || value == "" {
		return ""
	}

	// If it's an internal Hoop tag: hoop.dev/group.key
	if strings.HasPrefix(key, "hoop.dev/") {
		// Remove the "hoop.dev/" prefix
		remaining := strings.TrimPrefix(key, "hoop.dev/")

		// Find the last dot that separates the group from the key
		lastDotIndex := strings.LastIndex(remaining, ".")
		if lastDotIndex != -1 && lastDotIndex < len(remaining)-1 {
			// Extract only the key (after the last dot)
			userKey := remaining[lastDotIndex+1:]
			return fmt.Sprintf("%s=%s", userKey, value)
		}
	}

	// For custom tags, keep as is
	return fmt.Sprintf("%s=%s", key, value)
}

// formatTagString formats a tag string that is already in "key=value" format
func formatTagString(tagStr string) string {
	// If it already contains =, split and process
	if strings.Contains(tagStr, "=") {
		parts := strings.SplitN(tagStr, "=", 2)
		if len(parts) == 2 {
			return formatSingleTag(parts[0], parts[1])
		}
	}

	// If it doesn't contain =, assume it's a simple custom tag
	return tagStr
}
