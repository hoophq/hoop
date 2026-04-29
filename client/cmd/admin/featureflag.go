package admin

import (
	"fmt"
	"path"
	"text/tabwriter"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/spf13/cobra"
)

var ffOutputFlag string

func init() {
	featureFlagCmd.AddCommand(ffListCmd)
	featureFlagCmd.AddCommand(ffGetCmd)
	featureFlagCmd.AddCommand(ffEnableCmd)
	featureFlagCmd.AddCommand(ffDisableCmd)
	ffListCmd.Flags().StringVarP(&ffOutputFlag, "output", "o", "", "Output format. One of: (json)")
	ffGetCmd.Flags().StringVarP(&ffOutputFlag, "output", "o", "", "Output format. One of: (json)")
}

var featureFlagCmd = &cobra.Command{
	Use:     "feature-flag",
	Short:   "Manage feature flags",
	Aliases: []string{"feature-flags", "ff"},
}

var ffListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all feature flags with their current state",
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		decodeTo := "list"
		if ffOutputFlag == "json" {
			decodeTo = "raw"
		}
		obj, _, err := httpRequest(&apiResource{
			suffixEndpoint: "/api/feature-flags",
			conf:           conf,
			decodeTo:       decodeTo,
		})
		if err != nil {
			fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed listing feature flags: %v", err)))
			return
		}
		if ffOutputFlag == "json" {
			rawData, _ := obj.([]byte)
			fmt.Println(string(rawData))
			return
		}
		items, _ := obj.([]map[string]any)
		if len(items) == 0 {
			fmt.Println("No feature flags found")
			return
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTABILITY\tENABLED\tDESCRIPTION")
		for _, item := range items {
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
				item["name"],
				item["stability"],
				item["enabled"],
				item["description"],
			)
		}
		w.Flush()
	},
}

var ffGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a single feature flag",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		decodeTo := "list"
		if ffOutputFlag == "json" {
			decodeTo = "raw"
		}
		obj, _, err := httpRequest(&apiResource{
			suffixEndpoint: "/api/feature-flags",
			conf:           conf,
			decodeTo:       decodeTo,
		})
		if err != nil {
			fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed getting feature flags: %v", err)))
			return
		}
		if ffOutputFlag == "json" {
			rawData, _ := obj.([]byte)
			fmt.Println(string(rawData))
			return
		}
		items, _ := obj.([]map[string]any)
		for _, item := range items {
			if fmt.Sprintf("%v", item["name"]) == args[0] {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "NAME\tSTABILITY\tENABLED\tDESCRIPTION")
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
					item["name"],
					item["stability"],
					item["enabled"],
					item["description"],
				)
				w.Flush()
				return
			}
		}
		fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("feature flag %q not found", args[0])))
	},
}

var ffEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a feature flag",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		toggleFeatureFlag(args[0], true)
	},
}

var ffDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a feature flag",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		toggleFeatureFlag(args[0], false)
	},
}

func toggleFeatureFlag(name string, enabled bool) {
	conf := clientconfig.GetClientConfigOrDie()
	apir := &apiResource{
		suffixEndpoint: path.Join("/api/feature-flags", name),
		conf:           conf,
	}
	body := map[string]any{"enabled": enabled}
	resp, err := httpBodyRequest(apir, "PUT", body)
	if err != nil {
		fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed updating feature flag: %v", err)))
		return
	}
	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	if respMap, ok := resp.(map[string]any); ok {
		fmt.Printf("feature flag '%v' %s\n", respMap["name"], action)
	} else {
		fmt.Printf("feature flag '%s' %s\n", name, action)
	}
}
