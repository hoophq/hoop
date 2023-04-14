package cmd

import "github.com/runopsio/hoop/client/cmd/admin"

func init() {
	rootCmd.AddCommand(admin.MainCmd)
}
