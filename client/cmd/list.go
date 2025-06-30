package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var (
	verboseFlag bool
	outputFlag  string
)

var listExampleDesc = `hoop list
hoop ls
hoop list --verbose
hoop list -o json`

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List available connections",
	Long:    "Display connections that you have access to connect",
	Example: listExampleDesc,
	Run: func(cmd *cobra.Command, args []string) {
		runList()
	},
}

func init() {
	listCmd.Flags().BoolVarP(&verboseFlag, "verbose", "v", false, "Show additional information including command")
	listCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One of: (json)")
	rootCmd.AddCommand(listCmd)
}

func runList() {
	config := clientconfig.GetClientConfigOrDie()

	connections, err := fetchConnections(config)
	if err != nil {
		printErrorAndExit("Failed to fetch connections: %v", err)
	}

	if outputFlag == "json" {
		jsonData, err := json.MarshalIndent(connections, "", "  ")
		if err != nil {
			printErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Print(string(jsonData))
		return
	}

	displayConnections(connections)
}

func fetchConnections(config *clientconfig.Config) ([]map[string]any, error) {
	url := config.ApiURL + "/api/connections"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %v", err)
	}
	defer resp.Body.Close()

	log.Debugf("http response %v", resp.StatusCode)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch connections, status=%v", resp.StatusCode)
	}

	var connections []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&connections); err != nil {
		return nil, fmt.Errorf("failed decoding response: %v", err)
	}

	return connections, nil
}

func displayConnections(connections []map[string]any) {
	if len(connections) == 0 {
		fmt.Println("No connections available")
		return
	}

	// Fetch agent information
	config := clientconfig.GetClientConfigOrDie()
	agentInfo := fetchAgentInfo(config)

	w := tabwriter.NewWriter(os.Stdout, 6, 4, 3, ' ', tabwriter.TabIndent)
	defer w.Flush()

	if verboseFlag {
		fmt.Fprintln(w, "NAME\tCOMMAND\tTYPE\tAGENT\tSTATUS\tTAGS\t")
	} else {
		fmt.Fprintln(w, "NAME\tTYPE\tAGENT\tSTATUS\t")
	}

	// Sort connections by name
	sortConnections(connections)

	for _, conn := range connections {
		name := toStr(conn["name"])
		connType := toStr(conn["type"])
		status := toStr(conn["status"])
		agentID := toStr(conn["agent_id"])

		// Get agent name
		agentName := getAgentName(agentInfo, agentID)

		if verboseFlag {
			cmdList, _ := conn["command"].([]any)
			command := joinCommand(cmdList)
			tags := formatConnectionTags(conn["connection_tags"])
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t",
				name, command, connType, agentName, status, tags)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t",
				name, connType, agentName, status)
		}
		fmt.Fprintln(w)
	}
}

func fetchAgentInfo(config *clientconfig.Config) map[string]map[string]any {
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
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", version.Get().Version))

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

func getAgentName(agentInfo map[string]map[string]any, agentID string) string {
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

func sortConnections(connections []map[string]any) {
	sort.Slice(connections, func(i, j int) bool {
		nameI := toStr(connections[i]["name"])
		nameJ := toStr(connections[j]["name"])
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})
}

func joinCommand(cmdList []any) string {
	if len(cmdList) == 0 {
		return "-"
	}

	var list []string
	for _, c := range cmdList {
		list = append(list, fmt.Sprintf("%q", c))
	}
	cmd := strings.Join(list, " ")

	// Truncate to keep the table readable (more aggressive than admin)
	if len(cmd) > 25 {
		cmd = cmd[:22] + "..."
	}

	return fmt.Sprintf("[ %s ]", cmd)
}

func toStr(v any) string {
	s := fmt.Sprintf("%v", v)
	if s == "" || s == "<nil>" || v == nil {
		return "-"
	}
	return s
}

func formatConnectionTags(tags any) string {
	switch v := tags.(type) {
	case map[string]any:
		if len(v) == 0 {
			return "-"
		}
		var tagPairs []string
		for key, val := range v {
			formattedTag := formatSingleTagForList(key, toStr(val))
			if formattedTag != "" {
				tagPairs = append(tagPairs, formattedTag)
			}
		}
		if len(tagPairs) == 0 {
			return "-"
		}
		sort.Strings(tagPairs)
		tags := strings.Join(tagPairs, ", ")
		// Truncate long tags to keep the table readable
		if len(tags) > 30 {
			tags = tags[:27] + "..."
		}
		return tags
	default:
		return "-"
	}
}

// formatSingleTagForList formats an individual tag for the list command
func formatSingleTagForList(key, value string) string {
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
