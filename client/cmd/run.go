package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/runopsio/hoop/agent"
	"github.com/runopsio/hoop/client/cmd/admin"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
)

var runFlags = struct {
	Name             string
	EnvExport        string
	ConnectionString string
	Reviewers        []string
	RedactTypes      []string
}{}

var exampleRunFlag = `
hoop run --database 'postgres://user:paswd@externalhost:5432/mydb'
hoop run --name shell-console bash
hoop run rails console
hoop run -- kubectl exec -it deploy/myapp -- bash
`

func init() {
	os := appruntime.OS()
	runCmd.Flags().StringVarP(&runFlags.Name, "name", "n", os["hostname"], "The name of the connection resource, defaults to local hostname")
	runCmd.Flags().StringVar(&runFlags.EnvExport, "export", "", "Which envs to export from the host. e.g.: --export HOSTNAME,TERM. To expose all use --export _all")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "postgres", "", "The database connection uri, e.g.: postgres://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mysql", "", "The database connection uri, e.g.: mysql://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mssql", "", "The database connection uri, e.g.: sqlserver://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mongodb", "", "The database connection uri, e.g.: mongodb://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "database", "", "Generic option for providing a database connection, e.g.: (postgres://,mysql://,sqlserver://,mongodb://)")
	runCmd.Flags().StringSliceVar(&runFlags.Reviewers, "review", nil, "The approval groups for this connection, interactions are reviewed when enabled")
	runCmd.Flags().StringSliceVar(&runFlags.RedactTypes, "data-masking", nil, "The data masking types for this connection, content is redacted when enabled")

	_ = runCmd.Flags().MarkHidden("export")
	rootCmd.AddCommand(runCmd)
}

var runCmd = &cobra.Command{
	Use:                   "run [FLAGS] [COMMAND ...]",
	Short:                 "Connect to internal services",
	Example:               exampleRunFlag,
	DisableFlagParsing:    false,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(runFlags.Name) >= 253 {
			fmt.Println("--name flag cannot be longer than 128 characters")
			os.Exit(1)
		}
		// this shouldn't happen, it should have a default based on the hostname
		if runFlags.Name == "" {
			fmt.Println("It was not possible to retrieve the hostname. Run with --name YOUR_NAME")
			os.Exit(1)
		}

		request := proto.PreConnectRequest{
			Name:        admin.NormalizeResourceName(runFlags.Name),
			Type:        "custom",
			Reviewers:   runFlags.Reviewers,
			RedactTypes: runFlags.RedactTypes,
		}
		switch {
		case runFlags.ConnectionString != "":
			if err := setDatabaseType(&request); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		case len(args) > 0:
			request.Command = args
			request.Type = "custom"
		default:
			defaultShellPath, err := parseDefaultShell()
			if err != nil {
				fmt.Println("fail to obtain default shell (bash, sh) based on your $PATH")
				os.Exit(1)
			}
			request.Command = []string{defaultShellPath}
		}
		agent.RunV2(&request, parseHostEnvs())
	},
}

func setDatabaseType(req *proto.PreConnectRequest) (err error) {
	if req.Envs == nil {
		req.Envs = map[string]string{}
	}
	req.Type = "database"
	u, err := url.Parse(runFlags.ConnectionString)
	if err != nil {
		return
	}
	if u.User == nil {
		return fmt.Errorf("missing user credentials information on the connection string")
	}
	passwd, isset := u.User.Password()
	if !isset {
		return fmt.Errorf("password is not set on the connection string")
	}
	port := u.Port()
	dbname := strings.TrimPrefix(u.Path, "/")
	if !strings.Contains(dbname, "/") && dbname != "" {
		req.Envs["envvar:DB"] = encb64(strings.TrimPrefix(u.Path, "/"))
	}
	switch {
	case strings.HasSuffix(u.Scheme, "postgres"):
		req.Subtype = proto.ConnectionTypePostgres.String()
		req.Command = []string{"psql", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
		if port == "" {
			port = "5432"
		}
	case strings.HasSuffix(u.Scheme, "mysql"):
		req.Subtype = proto.ConnectionTypeMySQL.String()
		req.Command = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
		if port == "" {
			port = "3306"
		}
	case strings.HasSuffix(u.Scheme, "mongodb"):
		req.Subtype = proto.ConnectionTypeMongo.String()
		req.Command = []string{"mongo", "--quiet", "mongodb://$USER:$PASS@$HOST:$PORT/"}
		if port == "" {
			port = "27017"
		}
	case strings.HasSuffix(u.Scheme, "sqlserver"):
		req.Subtype = proto.ConnectionTypeMSSQL.String()
		req.Command = []string{"sqlcmd", "--exit-on-error", "--trim-spaces", "-r", "-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
		if port == "" {
			port = "1433"
		}
		req.Envs["envvar:INSECURE"] = encb64(u.Query().Get("insecureSkipVerify") == "true")
		dbname = u.Query().Get("database")
		if dbname != "" {
			req.Envs["envvar:DB"] = encb64(u.Query().Get("database"))
		}
	default:
		return fmt.Errorf("unknown database type, scheme=%v", u.Scheme)
	}

	req.Envs["envvar:HOST"] = encb64(u.Hostname())
	req.Envs["envvar:PORT"] = encb64(port)
	req.Envs["envvar:USER"] = encb64(u.User.Username())
	req.Envs["envvar:PASS"] = encb64(passwd)
	return
}

func parseHostEnvs() (envs []string) {
	if runFlags.EnvExport == "_all" {
		return os.Environ()
	}
	for _, envKey := range strings.Split(runFlags.EnvExport, ",") {
		if val, exists := os.LookupEnv(envKey); exists {
			envs = append(envs, fmt.Sprintf("%s=%s", envKey, val))
		}
	}
	return
}

func parseDefaultShell() (shellPath string, err error) {
	shellPath, err = exec.LookPath("bash")
	if errors.Is(err, exec.ErrNotFound) {
		shellPath, err = exec.LookPath("sh")
	}
	return
}

func encb64(v any) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", v)))
}
