package admin

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/log"
	"github.com/spf13/cobra"
)

func init() {
	getCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One off: (json)")
}

var getLongDesc = `Display one or many resources. Available ones:

* agents (tabview)
* connections (tabview)
* jits
* reviews
* plugins (tabview)
* runbooks
* sessions
* sessionstatus (tabview)
* users (tabview)
`

var getExamplesDesc = `
hoop admin get agents
hoop admin get connections -o json
hoop admin get plugins`

var getCmd = &cobra.Command{
	Use:     "get RESOURCE",
	Short:   "Display one or many resources",
	Long:    getLongDesc,
	Example: getExamplesDesc,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name.")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		apir := parseResourceOrDie(args, "GET", outputFlag)
		obj, err := httpRequest(apir)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := obj.([]byte)
			fmt.Print(string(jsonData))
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 6, 4, 3, ' ', tabwriter.TabIndent)
		defer w.Flush()
		switch apir.resourceType {
		case "agent", "agents":
			fmt.Fprintln(w, "UID\tNAME\tVERSION\tHOSTNAME\tPLATFORM\tSTATUS\t")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t",
					m["id"], m["name"], m["version"], m["hostname"], m["platform"], normalizeStatus(m["status"]))
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t",
						m["id"], m["name"], m["version"], m["hostname"], m["platform"], normalizeStatus(m["status"]))
					fmt.Fprintln(w)
				}
			}
		case "conn", "connection", "connections":
			agentHandlerFn := agentConnectedHandler(apir.conf)
			plugingHandlerFn := pluginHandler(apir)
			fmt.Fprintln(w, "NAME\tCOMMAND\tTYPE\tAGENT\tSTATUS\tSECRETS\tPLUGINS\t")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				enabledPlugins := plugingHandlerFn(fmt.Sprintf("%v", m["name"]))
				agentID := fmt.Sprintf("%v", m["agent_id"])
				status := agentHandlerFn("status", agentID)
				agentName := agentHandlerFn("name", agentID)
				secrets, _ := m["secret"].(map[string]any)
				cmdList, _ := m["command"].([]any)
				cmd := joinCmd(cmdList)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t",
					m["name"], cmd, m["type"], agentName, status, len(secrets), enabledPlugins)
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					enabledPlugins := plugingHandlerFn(fmt.Sprintf("%v", m["name"]))
					agentID := fmt.Sprintf("%v", m["agent_id"])
					status := agentHandlerFn("status", agentID)
					agentName := agentHandlerFn("name", agentID)
					cmdList, _ := m["command"].([]any)
					cmd := joinCmd(cmdList)
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t",
						m["name"], cmd, m["type"], agentName, status, "-", enabledPlugins)
					fmt.Fprintln(w)
				}
			}
		case "plugin", "plugins":
			fmt.Fprintln(w, "NAME\tSOURCE\tPRIORITY\tCONNECTIONS\tCONFIG")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				connections := len(m["connections"].([]any))
				source := "-"
				if m["source"] != nil {
					source = fmt.Sprintf("%v", m["source"])
				}
				configID := mapGetter("id", m["config"])
				if configID == "" {
					configID = "-"
				}
				fmt.Fprintf(w, "%s\t%v\t%v\t%v\t%v\t",
					m["name"], source, m["priority"], connections, configID)
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					connections := len(m["connections"].([]any))
					source := "-"
					if m["source"] != nil {
						source = fmt.Sprintf("%v", m["source"])
					}
					configID := mapGetter("id", m["config"])
					if configID == "" {
						configID = "-"
					}
					fmt.Fprintf(w, "%s\t%v\t%v\t%v\t%v\t",
						m["name"], source, m["priority"], connections, configID)
					fmt.Fprintln(w)
				}
			}
		case "sessionstatus":
			fmt.Fprintln(w, "SESSION\tPHASE\tERROR\tTIME\t")
			contents, _ := obj.([]map[string]any)
			for _, m := range contents {
				id := mapGetter("id", m["status"])
				phase := mapGetter("phase", m["status"])
				errorMsg := mapGetter("error", m["status"])
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t", id, phase, errorMsg, m["tx_time"])
				fmt.Fprintln(w)
			}
		case "user", "users", "userinfo":
			fmt.Fprintln(w, "ID\tEMAIL\tNAME\tSLACKID\tSTATUS\tGROUPS")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				groupList := joinGroups(m["groups"].([]any))
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%v\t", m["id"], m["email"], m["name"], m["slack_id"], m["status"], groupList)
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					groupList := joinGroups(m["groups"].([]any))
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%v\t", m["id"], m["email"], m["name"], m["slack_id"], m["status"], groupList)
					fmt.Fprintln(w)
				}
			}
		default:
			styles.PrintErrorAndExit("tab view not implemented for resource type %q, try repeating the command with the -o json option.", apir.resourceType)
		}
	},
}

func mapGetter(key string, obj any) string {
	objMap, ok := obj.(map[string]any)
	if !ok {
		return ""
	}
	val := objMap[key]
	if val == nil {
		return "-"
	}
	return fmt.Sprintf("%v", val)
}

func normalizeStatus(status any) string {
	switch fmt.Sprintf("%v", status) {
	case "CONNECTED":
		return "ONLINE"
	case "DISCONNECTED":
		return "OFFLINE"
	case "":
		return "-"
	default:
		return "UNKNOWN"
	}
}

func pluginHandler(apir *apiResource) func(connectionName string) string {
	data, err := httpRequest(&apiResource{suffixEndpoint: "/api/plugins", conf: apir.conf, decodeTo: "list"})
	if err != nil {
		log.Debugf("failed retrieving list of plugins, err=%v", err)
	}
	contents, ok := data.([]map[string]any)
	if !ok {
		log.Debugf("failed type casting to []map[string]any")
	}
	return func(connectionName string) string {
		if err != nil || len(contents) == 0 {
			return "-"
		}
		enabledPluginsMap := map[string]any{}
		for _, m := range contents {
			connList, _ := m["connections"].([]any)
			for _, connNameObj := range connList {
				connName := fmt.Sprintf("%v", connNameObj)
				if connName == connectionName {
					pluginName := fmt.Sprintf("%v", m["name"])
					enabledPluginsMap[pluginName] = nil
				}
			}
		}
		if len(enabledPluginsMap) == 0 {
			return "-"
		}
		var enabledPlugins []string
		for pluginName := range enabledPluginsMap {
			enabledPlugins = append(enabledPlugins, pluginName)
		}
		sort.Strings(enabledPlugins)
		return strings.Join(enabledPlugins, ", ")
	}
}

func agentConnectedHandler(conf *clientconfig.Config) func(key, agentID string) string {
	data, err := httpRequest(&apiResource{suffixEndpoint: "/api/agents", conf: conf, decodeTo: "list"})
	if err != nil {
		log.Debugf("failed retrieving list of connected agents, err=%v", err)
	}
	contents, ok := data.([]map[string]any)
	if !ok {
		log.Debugf("failed type casting to []map[string]any")
	}
	return func(key, agentID string) string {
		switch key {
		case "status":
			if err != nil || contents == nil {
				return normalizeStatus("UNKNOWN")
			}
			for _, m := range contents {
				if m["id"] == agentID {
					return normalizeStatus(m["status"])
				}
			}
			return normalizeStatus("UNKNOWN")
		case "name":
			if err != nil || contents == nil {
				return "UNKNOWN"
			}
			for _, m := range contents {
				if m["id"] == agentID {
					return fmt.Sprintf("%v", m["name"])
				}
			}
			return "-"
		}
		return "UNKNOWN"
	}
}

func joinCmd(cmdList []any) string {
	var list []string
	for _, c := range cmdList {
		list = append(list, c.(string))
	}
	cmd := strings.Join(list, " ")
	if len(cmd) > 30 {
		cmd = cmd[0:30] + "..."
	}
	return cmd
}

func joinGroups(groupList []any) string {
	var list []string
	for _, c := range groupList {
		list = append(list, c.(string))
	}
	return strings.Join(list, ", ")
}
