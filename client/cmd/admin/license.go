package admin

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var (
	licenseFileFlag             string
	licenseSignAllowedHostsFlag []string
	licenseSignDescFlag         string
	licenseSignExpireAtFlag     string
)

const (
	LicenseOSSType        string = "oss"
	LicenseEnterpriseType string = "enterprise"
)

func init() {
	licenseInstallCmd.Flags().StringVar(&licenseFileFlag, "file", "", "The file containing the license data")

	licenseSignCmd.Flags().StringSliceVar(&licenseSignAllowedHostsFlag, "allowed-hosts", nil, "The allowed hosts allowed to use this license. The value * allows all hosts")
	licenseSignCmd.Flags().StringVar(&licenseSignDescFlag, "description", "", "A description why this license was issued")
	licenseSignCmd.Flags().StringVar(&licenseSignExpireAtFlag, "expire-at", "8640h", "The license expiration time")

	licenseCmd.AddCommand(licenseSignCmd)
	licenseCmd.AddCommand(licenseInstallCmd)
}

var licenseCmd = &cobra.Command{
	Use:    "license NAME",
	Short:  "Manage license in a hoop gateway instance",
	Hidden: false,
}

var licenseSignCmd = &cobra.Command{
	Use:   "sign [oss|enterprise]",
	Short: "Generate and sign a license for a customer",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			styles.PrintErrorAndExit("missing the license type argument, oss|enterprise")
		}
		if len(licenseSignAllowedHostsFlag) == 0 {
			styles.PrintErrorAndExit("missing the --allowed-hosts flag")
		}
		if len(licenseSignDescFlag) == 0 {
			styles.PrintErrorAndExit("missing a description, use --description '<your-description>' flag")
		}

		apir := parseResourceOrDie([]string{"orglicense"}, "POST", "json")
		req := map[string]any{
			"license_type":  args[0],
			"allowed_hosts": licenseSignAllowedHostsFlag,
			"description":   licenseSignDescFlag,
			"expire_at":     licenseSignExpireAtFlag,
		}
		resp, err := httpBodyRequest(apir, "POST", req)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		licenseJsonBytes, ok := resp.([]byte)
		if !ok {
			styles.PrintErrorAndExit("unable to coerce to []byte, got=%T", resp)
		}
		fmt.Println(string(licenseJsonBytes))
	},
}

var licenseInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a license to a hoop gateway instance",
	Run: func(cmd *cobra.Command, args []string) {
		if licenseFileFlag == "" {
			styles.PrintErrorAndExit("missing --file flag")
		}
		apir := parseResourceOrDie([]string{"orglicense"}, "PUT", "raw")
		licenseData, err := os.ReadFile(licenseFileFlag)
		if err != nil {
			styles.PrintErrorAndExit("failed loading license file: %v", err)
		}

		var licenseJsonMap map[string]any
		if err := json.Unmarshal(licenseData, &licenseJsonMap); err != nil {
			styles.PrintErrorAndExit("failed decoding license: %v", err)
		}
		if _, err := httpBodyRequest(apir, "PUT", licenseJsonMap); err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		fmt.Println("license updated!")
	},
}
