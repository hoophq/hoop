package admin

import (
	"fmt"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var createOrgKeyCmd = &cobra.Command{
	Use:     "orgkeys",
	Short:   "Create a default organization token.",
	Aliases: []string{"orgkey"},
	Run: func(cmd *cobra.Command, args []string) {
		apir := parseResourceOrDie([]string{"orgkeys"}, "POST", outputFlag)
		resp, err := httpBodyRequest(apir, "POST", nil)
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
		fmt.Printf("%v\n", respMap["key"])
	},
}
