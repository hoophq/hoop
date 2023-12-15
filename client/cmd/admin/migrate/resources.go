package migrate

import (
	"net/url"
	"os"
	"time"

	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/pgrest/xtdbmigration"
	"github.com/spf13/cobra"
)

var MainCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate hoop resources",
}

var (
	roleNameFlag      string
	jwtSecretKeyFlag  string
	xtdbURLFlag       string
	orgNameFlag       string
	fromDateFlag      string
	dryRunFlag        bool
	maxSizeFlag       int64
	sessionIDListFlag []string
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

	reviewResourcesCmd.Flags().StringVar(&roleNameFlag, "role", defaultPgRestRole(), "The role name to use when connecting to postgrest")
	reviewResourcesCmd.Flags().StringVar(&jwtSecretKeyFlag, "key", defaultJwtSecretKey(), "The jwt secret key to generate access tokens")
	reviewResourcesCmd.Flags().StringVar(&xtdbURLFlag, "xtdb-url", os.Getenv("XTDB_ADDRESS"), "The address of the xtdb server")
	reviewResourcesCmd.Flags().StringVar(&orgNameFlag, "org", "default", "The name of the organization to migrate")

	sessionResourcesCmd.Flags().StringVar(&roleNameFlag, "role", defaultPgRestRole(), "The role name to use when connecting to postgrest")
	sessionResourcesCmd.Flags().StringVar(&jwtSecretKeyFlag, "key", defaultJwtSecretKey(), "The jwt secret key to generate access tokens")
	sessionResourcesCmd.Flags().StringVar(&xtdbURLFlag, "xtdb-url", os.Getenv("XTDB_ADDRESS"), "The address of the xtdb server")
	sessionResourcesCmd.Flags().StringVar(&orgNameFlag, "org", "default", "The name of the organization to migrate")
	sessionResourcesCmd.Flags().StringVar(&fromDateFlag, "from-date", "", "The timestamp to start migrating sessions from, format: YYYY-MM-DDTHH:MM:SSZ")
	sessionResourcesCmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "Don't run the migration, just count the sessions")
	sessionResourcesCmd.Flags().Int64Var(&maxSizeFlag, "max-size", 0, "The max size of sessions to process, defaults to no limit")
	sessionResourcesCmd.Flags().StringSliceVarP(&sessionIDListFlag, "sid", "s", nil, "The list of session ids to migrate")

	MainCmd.AddCommand(coreResourcesCmd)
	MainCmd.AddCommand(sessionResourcesCmd)
	MainCmd.AddCommand(reviewResourcesCmd)
}

var coreResourcesCmd = &cobra.Command{
	Use:          "core",
	Short:        "Migrate core resources: org, users, serviceaccounts, agents, connections and plugins",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
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

var reviewResourcesCmd = &cobra.Command{
	Use:          "reviews",
	Aliases:      []string{"review"},
	Short:        "Migrate review resources",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		if roleNameFlag == "" || jwtSecretKeyFlag == "" {
			styles.PrintErrorAndExit("fail to obtain default role or jwt secret key, flags are required: --role, --key")
		}
		pgrest.SetRoleName(roleNameFlag)
		pgrest.SetJwtKey([]byte(jwtSecretKeyFlag))
		baseURL, _ := url.Parse("http://127.0.0.1:8008")
		pgrest.SetBaseURL(baseURL)
		xtdbmigration.RunReviews(xtdbURLFlag, orgNameFlag)
	},
}

var sessionResourcesCmd = &cobra.Command{
	Use:          "sessions",
	Aliases:      []string{"session"},
	Short:        "Migrate session resources",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		if roleNameFlag == "" || jwtSecretKeyFlag == "" {
			styles.PrintErrorAndExit("fail to obtain default role or jwt secret key, flags are required: --role, --key")
		}
		pgrest.SetRoleName(roleNameFlag)
		pgrest.SetJwtKey([]byte(jwtSecretKeyFlag))
		baseURL, _ := url.Parse("http://127.0.0.1:8008")
		pgrest.SetBaseURL(baseURL)
		// migrate by session id
		if len(sessionIDListFlag) > 0 {
			xtdbmigration.RunSessions(xtdbURLFlag, orgNameFlag, dryRunFlag, time.Time{}, maxSizeFlag, sessionIDListFlag...)
			return
		}
		// migrate by time range
		fromDate, err := time.Parse(time.RFC3339, fromDateFlag)
		if err != nil {
			styles.PrintErrorAndExit("fail to parse --from-date, err=%v", err)
		}
		xtdbmigration.RunSessions(xtdbURLFlag, orgNameFlag, dryRunFlag, fromDate.UTC(), maxSizeFlag)
	},
}
