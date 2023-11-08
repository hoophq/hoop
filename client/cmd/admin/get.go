package admin

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

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
* reviews
* plugins (tabview)
* policies (tabview)
* runbooks
* sessions
* users (tabview)
* serviceaccounts (tabview)
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
		obj, _, err := httpRequest(apir)
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
			fmt.Fprintln(w, "UID\tNAME\tMODE\tVERSION\tHOSTNAME\tPLATFORM\tSTATUS\t")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%v\t%v\t%s\t",
					m["id"], m["name"], m["mode"], toStr(m["version"]), toStr(m["hostname"]), toStr(m["platform"]), normalizeStatus(m["status"]))
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%v\t%v\t%s\t",
						m["id"], m["name"], m["mode"], toStr(m["version"]), toStr(m["hostname"]), toStr(m["platform"]), normalizeStatus(m["status"]))
					fmt.Fprintln(w)
				}
			}
		case "conn", "connection", "connections":
			agentHandlerFn := agentConnectedHandler(apir.conf)
			if policyHandlerFn, requestOK := policiesHandler(apir); requestOK || isApiV2 {
				fmt.Fprintln(w, "NAME\tCOMMAND\tTYPE\tREVIEW\tAGENT\tSTATUS\tPOLICIES\tUPDATED")
				switch contents := obj.(type) {
				case map[string]any:
					m := contents
					enabledPolicies := policyHandlerFn(fmt.Sprintf("%v", m["id"]), false)
					agentID := fmt.Sprintf("%v", m["agentId"])
					status := agentHandlerFn("status", agentID)
					agentName := agentHandlerFn("name", agentID)
					cmdList, _ := m["command"].([]any)
					cmd := joinCmd(cmdList, false)
					fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\t%s\t%v\t%s\t",
						m["name"], cmd, m["type"], m["hasReview"], agentName, status, enabledPolicies, absTime(m["updatedAt"]))
					fmt.Fprintln(w)
				case []map[string]any:
					for _, m := range contents {
						enabledPolicies := policyHandlerFn(fmt.Sprintf("%v", m["id"]), true)
						agentID := fmt.Sprintf("%v", m["agentId"])
						status := agentHandlerFn("status", agentID)
						agentName := agentHandlerFn("name", agentID)
						cmdList, _ := m["command"].([]any)
						cmd := joinCmd(cmdList, true)
						fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\t%s\t%v\t%s\t",
							m["name"], cmd, m["type"], m["hasReview"], agentName, status, enabledPolicies, absTime(m["updatedAt"]))
						fmt.Fprintln(w)
					}
				}
				return
			}
			// fallback to plugins
			plugingHandlerFn := pluginHandler(apir)
			fmt.Fprintln(w, "NAME\tCOMMAND\tTYPE\tAGENT\tSTATUS\tSECRETS\tPLUGINS\t")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				enabledPlugins := plugingHandlerFn(fmt.Sprintf("%v", m["name"]), false)
				agentID := fmt.Sprintf("%v", m["name"])
				status := agentHandlerFn("status", agentID)
				agentName := agentHandlerFn("name", agentID)
				secrets, _ := m["secret"].(map[string]any)
				if secrets == nil {
					secrets, _ = m["secrets"].(map[string]any)
				}
				cmdList, _ := m["command"].([]any)
				cmd := joinCmd(cmdList, false)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t",
					m["name"], cmd, m["type"], agentName, status, len(secrets), enabledPlugins)
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					enabledPlugins := plugingHandlerFn(fmt.Sprintf("%v", m["name"]), true)
					agentID := fmt.Sprintf("%v", m["agent_id"])
					status := agentHandlerFn("status", agentID)
					agentName := agentHandlerFn("name", agentID)
					cmdList, _ := m["command"].([]any)
					cmd := joinCmd(cmdList, true)
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
		case "policies", "policy":
			fmt.Fprintln(w, "ID\tNAME\tTYPE\tCONNECTIONS\tCONFIG\tUPDATED")
			contents, _ := obj.([]map[string]any)
			for _, m := range contents {
				connections := len(m["connections"].([]any))
				pconf := joinList(m["config"], true)
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%v\t%s", m["id"], m["name"], m["type"], connections, pconf, absTime(m["updatedAt"]))
				fmt.Fprintln(w)
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
			switch contents := obj.(type) {
			case map[string]any:
				fmt.Fprintln(w, "ID\tEMAIL\tNAME\tSLACKID\tSTATUS\tAPIV2\tGROUPS\t")
				m := contents
				groupsObject, _ := m["groups"].([]any)
				groupList := joinItems(groupsObject)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%v\t%v\t", m["id"], m["email"], m["name"], m["slack_id"], m["status"], m["is_apiv2"], groupList)
				fmt.Fprintln(w)
			case []map[string]any:
				fmt.Fprintln(w, "ID\tEMAIL\tNAME\tSLACKID\tSTATUS\tGROUPS\t")
				for _, m := range contents {
					groupsObject, _ := m["groups"].([]any)
					groupList := joinItems(groupsObject)
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%v\t", m["id"], m["email"], m["name"], m["slack_id"], m["status"], groupList)
					fmt.Fprintln(w)
				}
			}
		case "serviceaccount", "serviceaccounts", "sa":
			switch contents := obj.(type) {
			case map[string]any:
				fmt.Fprintln(w, "SUBJECT\tNAME\tSTATUS\tGROUPS\t")
				m := contents
				groupsObject, _ := m["groups"].([]any)
				groupList := joinItems(groupsObject)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t", m["subject"], m["name"], m["status"], groupList)
				fmt.Fprintln(w)
			case []map[string]any:
				fmt.Fprintln(w, "SUBJECT\tNAME\tSTATUS\tGROUPS\t")
				for _, m := range contents {
					groupsObject, _ := m["groups"].([]any)
					groupList := joinItems(groupsObject)
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t", m["subject"], m["name"], m["status"], groupList)
					fmt.Fprintln(w)
				}
			}
		case "clientkeys":
			fmt.Fprintln(w, "NAME\tENABLED")
			switch contents := obj.(type) {
			case map[string]any:
				m := contents
				fmt.Fprintf(w, "%s\t%v\t", m["name"], m["active"])
				fmt.Fprintln(w)
			case []map[string]any:
				for _, m := range contents {
					fmt.Fprintf(w, "%s\t%v\t", m["name"], m["active"])
					fmt.Fprintln(w)
				}
			}
		case "runbooks":
			switch contents := obj.(type) {
			case map[string]any:
				commit := fmt.Sprintf("%v", contents["commit"])
				if len(commit) > 7 {
					commit = commit[:7]
				}
				fmt.Fprintln(w, "NAME\tMETADATA\tCONNECTIONS\tCOMMIT")
				runbookList, _ := contents["items"].([]interface{})
				for _, obj := range runbookList {
					m, ok := obj.(map[string]any)
					if !ok {
						continue
					}
					metadata, _ := m["metadata"].(map[string]any)
					var metadataList []string
					for metakey := range metadata {
						metadataList = append(metadataList, metakey)
					}
					connections := "-"
					connectionList, _ := m["connections"].([]any)
					if connectionList != nil {
						connections = joinItems(connectionList)
					}
					fmt.Fprintf(w, "%v\t%v\t%v\t%s",
						m["name"],
						strings.Join(metadataList, ", "),
						connections,
						commit,
					)
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
	default:
		return "-"
	}
}

func toStr(v any) string {
	s := fmt.Sprintf("%v", v)
	if s == "" {
		return "-"
	}
	return s
}

func pluginHandler(apir *apiResource) func(connectionName string, trunc bool) string {
	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/plugins", conf: apir.conf, decodeTo: "list"})
	if err != nil {
		log.Debugf("failed retrieving list of plugins, err=%v", err)
	}
	contents, ok := data.([]map[string]any)
	if !ok {
		log.Debugf("failed type casting to []map[string]any")
	}

	return func(connectionName string, trunc bool) string {
		if err != nil || len(contents) == 0 {
			return "-"
		}
		enabledPluginsMap := map[string]any{}
		for _, m := range contents {
			connList, _ := m["connections"].([]any)
			for _, pluginConnObj := range connList {
				pluginConnMap, _ := pluginConnObj.(map[string]any)
				if pluginConnMap == nil {
					pluginConnMap = map[string]any{}
				}
				connName := fmt.Sprintf("%v", pluginConnMap["name"])
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
		plugins := strings.Join(enabledPlugins, ", ")
		if len(plugins) > 30 && trunc {
			plugins = plugins[0:30] + "..."
		}
		return fmt.Sprintf("(%v) %s", len(enabledPlugins), plugins)
	}
}

func agentConnectedHandler(conf *clientconfig.Config) func(key, agentID string) string {
	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/agents", conf: conf, decodeTo: "list"})
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
				return "-"
			}
			for _, m := range contents {
				if m["id"] == agentID {
					return normalizeStatus(m["status"])
				}
			}
			return normalizeStatus("UNKNOWN")
		case "name", "version", "hostname", "platform":
			if err != nil || contents == nil {
				return "-"
			}
			for _, m := range contents {
				if m["id"] == agentID {
					return fmt.Sprintf("%v", m[key])
				}
			}
			return "-"
		}
		return "-"
	}
}

func policiesHandler(apir *apiResource) (func(connectionID string, trunc bool) string, bool) {
	requestOK := true
	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/policies", conf: apir.conf, decodeTo: "list"})
	if err != nil {
		log.Debugf("failed retrieving list of policies, err=%v", err)
		requestOK = false
	}
	contents, ok := data.([]map[string]any)
	if !ok {
		log.Debugf("failed type casting to []map[string]any")
		requestOK = false
	}

	return func(connectionID string, trunc bool) string {
		if err != nil || len(contents) == 0 {
			return "-"
		}
		var policies []string
		for _, m := range contents {
			connList, _ := m["connections"].([]any)
			for _, connID := range connList {
				if connID == connectionID {
					policyName := fmt.Sprintf("%v", m["name"])
					policies = append(policies, policyName)
				}
			}
		}
		if len(policies) == 0 {
			return "-"
		}
		sort.Strings(policies)
		plugins := strings.Join(policies, ", ")
		if len(plugins) > 30 && trunc {
			plugins = plugins[0:30] + "..."
		}
		return fmt.Sprintf("(%v) %s", len(policies), plugins)
	}, requestOK
}

func joinCmd(cmdList []any, trunc bool) string {
	var list []string
	for _, c := range cmdList {
		list = append(list, fmt.Sprintf("%q", c))
	}
	cmd := strings.Join(list, " ")
	if len(cmd) > 30 && trunc {
		cmd = cmd[0:30] + "..."
	}
	return fmt.Sprintf("[ %s ]", cmd)
}

func joinItems(items []any) string {
	var list []string
	for _, c := range items {
		list = append(list, c.(string))
	}
	return strings.Join(list, ", ")
}

func joinList(v any, trunc bool) string {
	itemList, ok := v.([]any)
	if !ok {
		return "-"
	}
	var list []string
	for _, c := range itemList {
		list = append(list, fmt.Sprintf("%q", c))
	}
	cmd := strings.Join(list, " ")
	if len(cmd) > 30 && trunc {
		cmd = cmd[0:30] + "..."
	}
	return fmt.Sprintf("[ %s ]", cmd)
}

// absTime given v as a time string, parse to absolute time
func absTime(v any) string {
	t1, err := time.Parse(time.RFC3339Nano, v.(string))
	if err != nil {
		return "-"
	}
	t2 := time.Now().UTC().Sub(t1)
	switch {
	case t2.Seconds() <= 60:
		return fmt.Sprintf("%.0fs ago", t2.Seconds())
	case t2.Minutes() < 60: // minutes
		return fmt.Sprintf("%.0fm ago", t2.Minutes())
	case t2.Hours() < 24: // hours
		return fmt.Sprintf("%.0fh ago", t2.Hours())
	case t2.Hours() > 24: // days
		return fmt.Sprintf("%vd ago", math.Round(t2.Hours()/30))
	}
	return "-"
}
