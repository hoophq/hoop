package admin

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/spf13/cobra"
)

var (
	createAgentModeFlag      string
	createAgentTagsFlag      []string
	createAgentOverwriteFlag bool
)

func init() {
	createAgentCmd.Flags().StringVar(&createAgentModeFlag, "mode", pb.AgentModeStandardType, fmt.Sprintf("The agent mode operation (%s or %s)",
		pb.AgentModeStandardType, pb.AgentModeEmbeddedType))
	createAgentCmd.Flags().StringSliceVar(&createAgentTagsFlag, "tags", nil, "Tags to select agents in a key=value format")
	createAgentCmd.Flags().BoolVar(&createAgentOverwriteFlag, "overwrite", false,
		"If the agent exists, replace its tags with --tags (none removes them) and keep its key")
}

var createAgentCmd = &cobra.Command{
	Use:     "agent NAME",
	Short:   "Create an agent token.",
	Aliases: []string{"agents"},
	Example: `hoop admin create agent prod-eu --tags environment=prod,team=data
hoop admin create agent prod-eu --overwrite --tags environment=prod`,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		tags, err := parseTagsFlag(createAgentTagsFlag)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		args = []string{"agent", args[0]}
		apir := parseResourceOrDie(args, "POST", outputFlag)
		apir.name = NormalizeResourceName(apir.name)

		if createAgentOverwriteFlag {
			exists, err := getAgent(apir.conf, apir.name)
			if err != nil {
				styles.PrintErrorAndExit("failed retrieving agent %v, %v", apir.name, err)
			}
			if exists {
				if err := updateAgentTags(apir, tags); err != nil {
					styles.PrintErrorAndExit("%s", err.Error())
				}
				return
			}
		}

		body := map[string]any{"name": apir.name, "mode": createAgentModeFlag}
		if len(tags) > 0 {
			// A gateway without agent tags ignores the field and still creates
			// the agent: refuse before anything is created.
			if err := checkAgentTagsSupported(apir.conf); err != nil {
				styles.PrintErrorAndExit("no agent was created: %v", err)
			}
			body["tags"] = tags
		}
		resp, err := httpBodyRequest(apir, "POST", body)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		respMap, ok := resp.(map[string]any)
		if !ok {
			styles.PrintErrorAndExit("failed decoding response map")
		}
		fmt.Printf("%v\n", respMap["token"])
	},
}

// updateAgentTags replaces the tags of an existing agent and prints them.
func updateAgentTags(apir *apiResource, tags map[string]string) error {
	body := map[string]any{}
	for key, val := range tags {
		body[key] = val
	}
	apir.suffixEndpoint = fmt.Sprintf("/api/agents/%v/tags", apir.name)
	resp, err := httpBodyRequest(apir, "PUT", body)
	if err != nil {
		return explainTagsUpdateError(apir.conf, apir.name, err)
	}
	if apir.decodeTo == "raw" {
		jsonData, _ := resp.([]byte)
		fmt.Println(string(jsonData))
		return nil
	}
	respMap, _ := resp.(map[string]any)
	tagList := joinMap(respMap["tags"])
	if tagList == "" {
		tagList = "(none)"
	}
	fmt.Printf("agent %v updated, tags: %v\n", apir.name, tagList)
	return nil
}

// explainTagsUpdateError tells the causes of a 404 on PUT /agents/{name}/tags
// apart: an old gateway, or an agent deleted since the existence check.
func explainTagsUpdateError(conf *clientconfig.Config, name string, err error) error {
	if !strings.Contains(err.Error(), "status=404") {
		return err
	}
	switch supportErr := checkAgentTagsSupported(conf); {
	case errors.Is(supportErr, errGatewayWithoutAgentTags):
		return fmt.Errorf("the gateway does not support agent tags, upgrade it to edit them")
	case supportErr != nil:
		return fmt.Errorf("failed updating the tags of agent %v, the tags were not changed: %v; %v", name, err, supportErr)
	}
	return fmt.Errorf("agent %v not found: %v", name, err)
}

// parseTagsFlag reads key=value pairs. A pair without '=' or with an empty key
// is an error, so a typo does not create an agent without the tag.
func parseTagsFlag(pairs []string) (map[string]string, error) {
	tags := map[string]string{}
	for _, pair := range pairs {
		key, val, found := strings.Cut(pair, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid tag %q, use key=value", pair)
		}
		tags[key] = val
	}
	return tags, nil
}

func getAgent(conf *clientconfig.Config, name string) (bool, error) {
	_, err := getAgentObject(conf, name)
	if err != nil && strings.Contains(err.Error(), "status=404") {
		return false, nil
	}
	return err == nil, err
}

func getAgentObject(conf *clientconfig.Config, name string) (map[string]any, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/agents/%v", name),
		method:         "GET",
		conf:           conf,
		decodeTo:       "object"})
	if err != nil {
		return nil, err
	}
	obj, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed decoding response to object")
	}
	return obj, nil
}

// checkAgentTagsSupported asks for GET /agent-tags, which only a gateway with
// agent tags serves.
func checkAgentTagsSupported(conf *clientconfig.Config) error {
	_, _, err := httpRequest(&apiResource{
		suffixEndpoint: "/api/agent-tags",
		method:         "GET",
		conf:           conf,
		decodeTo:       "object"})
	if err != nil && strings.Contains(err.Error(), "status=404") {
		return errGatewayWithoutAgentTags
	}
	if err != nil {
		return fmt.Errorf("failed checking the gateway for agent tags: %v", err)
	}
	return nil
}

var errGatewayWithoutAgentTags = errors.New("the gateway does not support agent tags, upgrade it")

func errNoAgentTags(what string) error {
	return fmt.Errorf("%s: %w", what, errGatewayWithoutAgentTags)
}
