package migrate

import (
	"net/url"
	"os"

	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/pgrest/xtdbmigration"
	"github.com/spf13/cobra"
)

var MainCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate hoop resources",
}

var (
	roleNameFlag     string
	jwtSecretKeyFlag string
	xtdbURLFlag      string
	orgNameFlag      string
)

func defaultPgRestRole() string {
	roleName := os.Getenv("PGREST_ROLE")
	if roleName == "" {
		roleName = "hoop_apiuser"
	}
	return roleName
}

func defaultJwtSecretKey() string {
	jwtSecretKey, err := os.ReadFile("/app/pgrest-secret-key")
	if err != nil {
		return ""
	}
	return string(jwtSecretKey)
}

func init() {
	coreResourcesCmd.Flags().StringVar(&roleNameFlag, "role", defaultPgRestRole(), "The role name to use when connecting to postgrest")
	coreResourcesCmd.Flags().StringVar(&jwtSecretKeyFlag, "key", defaultJwtSecretKey(), "The jwt secret key to generate access tokens")
	coreResourcesCmd.Flags().StringVar(&xtdbURLFlag, "xtdb-url", os.Getenv("XTDB_ADDRESS"), "The address of the xtdb server")
	coreResourcesCmd.Flags().StringVar(&orgNameFlag, "org", "default", "The name of the organization to migrate")
	MainCmd.AddCommand(coreResourcesCmd)
}

var coreResourcesCmd = &cobra.Command{
	Use:          "core",
	Short:        "Migrate core resources: org, users, serviceaccounts, agents, connections and plugins",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		log.Debugf("starting migration, this a debuggg!!")
		if roleNameFlag == "" || jwtSecretKeyFlag == "" {
			styles.PrintErrorAndExit("fail to obtain default role or jwt secret key, flags are required: --role, --key")
		}
		pgrest.SetRoleName(roleNameFlag)
		pgrest.SetJwtKey([]byte(jwtSecretKeyFlag))
		baseURL, _ := url.Parse("http://127.0.0.1:8008")
		pgrest.SetBaseURL(baseURL)
		xtdbmigration.RunCore(xtdbURLFlag, orgNameFlag)
	},
}
