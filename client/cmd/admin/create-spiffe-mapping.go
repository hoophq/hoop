package admin

import (
	"fmt"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var (
	spiffeMappingTrustDomain   string
	spiffeMappingSPIFFEID      string
	spiffeMappingSPIFFEPrefix  string
	spiffeMappingAgentID       string
	spiffeMappingAgentTemplate string
	spiffeMappingGroups        []string
)

func init() {
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingTrustDomain, "trust-domain", "", "SPIFFE trust domain (e.g. customer.com). Required.")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingSPIFFEID, "spiffe-id", "", "Exact SPIFFE ID to match (mutually exclusive with --spiffe-prefix).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingSPIFFEPrefix, "spiffe-prefix", "", "SPIFFE ID prefix to match (mutually exclusive with --spiffe-id).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingAgentID, "agent-id", "", "Resolve matches to this agent ID (mutually exclusive with --agent-template).")
	createSpiffeMappingCmd.Flags().StringVar(&spiffeMappingAgentTemplate, "agent-template", "", "Template to derive agent name from the SPIFFE ID suffix, e.g. '{{.WorkloadIdentifier}}' (mutually exclusive with --agent-id).")
	createSpiffeMappingCmd.Flags().StringSliceVar(&spiffeMappingGroups, "groups", []string{}, "Groups granted to matching agents, e.g.: agents,workflow-automation")
}

var createSpiffeMappingCmd = &cobra.Command{
	Use:     "spiffe-mapping",
	Aliases: []string{"spiffemapping"},
	Short:   "Create a SPIFFE-ID to Hoop agent mapping.",
	Long: `Create a SPIFFE-ID to Hoop agent mapping.

Exactly one of --spiffe-id and --spiffe-prefix must be set.
Exactly one of --agent-id and --agent-template must be set.
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

		apir := parseResourceOrDie([]string{"spiffe-mappings"}, "POST", outputFlag)
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

		resp, err := httpBodyRequest(apir, "POST", body)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Println("spiffe mapping created")
	},
}
