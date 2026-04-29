package admin

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	spiffeMappingTrustDomain   string
	spiffeMappingSPIFFEID      string
	spiffeMappingSPIFFEPrefix  string
	spiffeMappingAgentID       string
	spiffeMappingAgentTemplate string
	spiffeMappingGroups        []string
	spiffeMappingOverwriteFlag bool
)

func init() {
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingTrustDomain, "trust-domain", "", "SPIFFE trust domain (e.g. customer.com). Required.")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingSPIFFEID, "spiffe-id", "", "Exact SPIFFE ID to match (mutually exclusive with --spiffe-prefix).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingSPIFFEPrefix, "spiffe-prefix", "", "SPIFFE ID prefix to match (mutually exclusive with --spiffe-id).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingAgentID, "agent-id", "", "Resolve matches to this agent ID (mutually exclusive with --agent-template).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingAgentTemplate, "agent-template", "", "Template to derive agent name from the SPIFFE ID suffix, e.g. '{{.WorkloadIdentifier}}' (mutually exclusive with --agent-id).")
	createSpiffeMappingCmd.Flags().StringSliceVar(&spiffeMappingGroups, "groups", []string{}, "Groups granted to matching agents, e.g.: agents,workflow-automation")
	createSpiffeMappingCmd.Flags().BoolVar(&spiffeMappingOverwriteFlag, "overwrite", false, "It will create or update if a mapping with the same trust domain and SPIFFE id/prefix already exists")
}

var createSpiffeMappingCmd = &cobra.Command{
	Use:     "spiffe-mapping",
	Aliases: []string{"spiffemapping"},
	Short:   "Create a SPIFFE-ID to Hoop agent mapping.",
	Long: `Create a SPIFFE-ID to Hoop agent mapping.

Exactly one of --spiffe-id and --spiffe-prefix must be set.
Exactly one of --agent-id and --agent-template must be set.

Unlike other resources, a SPIFFE mapping has no user-chosen name: it is
identified by the composite key (trust-domain, spiffe-id) or
(trust-domain, spiffe-prefix). Pass --overwrite to update an existing
mapping matched by that composite key instead of failing with a conflict.
`,
	Run: func(cmd *cobra.Command, args []string) {
		if spiffeMappingTrustDomain == "" {
			styles.PrintErrorAndExit("--trust-domain is required")
		}
		if (spiffeMappingSPIFFEID == "") == (spiffeMappingSPIFFEPrefix == "") {
			styles.PrintErrorAndExit("exactly one of --spiffe-id and --spiffe-prefix must be set")
		}
		if (spiffeMappingAgentID == "") == (spiffeMappingAgentTemplate == "") {
			styles.PrintErrorAndExit("exactly one of --agent-id and --agent-template must be set")
		}

		actionName := "created"
		method := "POST"
		resourceArgs := []string{"spiffe-mappings"}
		if spiffeMappingOverwriteFlag {
			id, err := findSpiffeMapping(clientconfig.GetClientConfigOrDie(),
				spiffeMappingTrustDomain, spiffeMappingSPIFFEID, spiffeMappingSPIFFEPrefix)
			if err != nil {
				styles.PrintErrorAndExit("failed looking up existing spiffe mapping: %v", err)
			}
			if id != "" {
				log.Debugf("spiffe mapping %v exists, updating", id)
				actionName = "updated"
				method = "PUT"
				resourceArgs = []string{"spiffe-mappings", id}
			}
		}

		apir := parseResourceOrDie(resourceArgs, method, outputFlag)
		body := map[string]any{
			"trust_domain": spiffeMappingTrustDomain,
			"groups":       spiffeMappingGroups,
		}
		if spiffeMappingSPIFFEID != "" {
			body["spiffe_id"] = spiffeMappingSPIFFEID
		}
		if spiffeMappingSPIFFEPrefix != "" {
			body["spiffe_prefix"] = spiffeMappingSPIFFEPrefix
		}
		if spiffeMappingAgentID != "" {
			body["agent_id"] = spiffeMappingAgentID
		}
		if spiffeMappingAgentTemplate != "" {
			body["agent_template"] = spiffeMappingAgentTemplate
		}

		resp, err := httpBodyRequest(apir, method, body)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("spiffe mapping %v\n", actionName)
	},
}

// findSpiffeMapping returns the ID of an existing mapping that matches
// (trustDomain, spiffeID) or (trustDomain, spiffePrefix), or "" when no
// such mapping exists. The API has no composite-key lookup endpoint, so
// we list and filter client-side; the list is small (one row per
// SPIFFE identity the org has registered).
func findSpiffeMapping(conf *clientconfig.Config, trustDomain, spiffeID, spiffePrefix string) (string, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: "/api/spiffe-mappings",
		method:         "GET",
		conf:           conf,
		decodeTo:       "list",
	})
	if err != nil {
		if strings.Contains(err.Error(), "status=404") {
			return "", nil
		}
		return "", err
	}
	items, ok := resp.([]map[string]any)
	if !ok {
		return "", fmt.Errorf("failed decoding response to list")
	}
	for _, m := range items {
		if asString(m["trust_domain"]) != trustDomain {
			continue
		}
		if spiffeID != "" && asString(m["spiffe_id"]) == spiffeID {
			return asString(m["id"]), nil
		}
		if spiffePrefix != "" && asString(m["spiffe_prefix"]) == spiffePrefix {
			return asString(m["id"]), nil
		}
	}
	return "", nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
