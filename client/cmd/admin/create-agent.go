package admin

import (
	"fmt"

	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var createAgentCmd = &cobra.Command{
	Use:     "agent NAME",
	Short:   "(DEPRECATED) Create an agent token.",
	Aliases: []string{"agents"},
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		args = []string{"agent", args[0]}
		apir := parseResourceOrDie(args, "POST", outputFlag)
		resp, err := httpBodyRequest(apir, "POST", map[string]any{"name": apir.name})
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
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
