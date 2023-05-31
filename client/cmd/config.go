package cmd

import "github.com/runopsio/hoop/client/cmd/config"

func init() {
	rootCmd.AddCommand(config.MainCmd)
}
