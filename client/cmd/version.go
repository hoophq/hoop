package cmd

import (
	"fmt"

	"github.com/runopsio/hoop/client/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:          "version",
	Short:        "Show version",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(string(version.JSON()))
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
