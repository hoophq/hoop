package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/client/cmd/admin"
	"github.com/hoophq/hoop/common/appruntime"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/syntax"
)

var runFlags = struct {
	Name             string
	EnvExport        string
	ConnectionString string
	Command          string
	Reviewers        []string
	RedactTypes      []string
}{}

var exampleRunFlag = `
hoop run --database 'postgres://user:paswd@externalhost:5432/mydb'
hoop run --name shell-console --command 'bash --verbose'

# run YOUR_COMMAND in foreground
hoop run --command 'rails console' -- YOUR_COMMAND --YOUR-FLAG
`

func init() {
	osruntime := appruntime.OS()
	dbConnectionURI := os.Getenv("HOOP_DB_URI")
	runCmd.Flags().StringVarP(&runFlags.Name, "name", "n", osruntime["hostname"], "The name of the connection resource, defaults to local hostname")
	runCmd.Flags().StringVar(&runFlags.EnvExport, "export", "", "Which envs to export from the host. e.g.: --export HOSTNAME,TERM. By defaut expose all")
	runCmd.Flags().StringVar(&runFlags.Command, "command", "", "The entrypoint command of the connection. Defaults to $SHELL or database type commands")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "postgres", dbConnectionURI, "The database connection uri, e.g.: postgres://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mysql", dbConnectionURI, "The database connection uri, e.g.: mysql://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mssql", dbConnectionURI, "The database connection uri, e.g.: sqlserver://...")
	runCmd.Flags().StringVar(&runFlags.ConnectionString, "mongodb", dbConnectionURI, "The database connection uri, e.g.: mongodb://...")
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
	PreRunE:               func(_ *cobra.Command, args []string) error { return validateCommand() },
	Run: func(cmd *cobra.Command, args []string) {
		request := proto.PreConnectRequest{
			Name:        admin.NormalizeResourceName(runFlags.Name),
			Type:        "custom",
			Reviewers:   runFlags.Reviewers,
			RedactTypes: runFlags.RedactTypes,
		}

		reqCommand, err := parsePosixCmd(runFlags.Command)
		if err != nil {
			log.Fatalf("failed parsing --command flag: %v", err)
		}

		if runFlags.ConnectionString != "" {
			if err := setDatabaseType(&request); err != nil {
				log.Fatal(err)
			}
		}

		// set if it's provide by flags
		if len(reqCommand) > 0 {
			request.Command = reqCommand
		}
		if len(request.Command) == 0 {
			defaultShellPath, err := parseDefaultShell()
			if err != nil {
				log.Fatal("fail to obtain default shell (bash, sh) based on your $PATH")
			}
			request.Command = []string{defaultShellPath}
		}
		agent.RunV2(&request, parseHostEnvs(), args)
	},
}

func validateCommand() (err error) {
	if len(runFlags.Name) >= 253 {
		return fmt.Errorf("--name flag cannot be longer than 253 characters")
	}
	// this shouldn't happen, it should have a default based on the hostname
	if runFlags.Name == "" {
		return fmt.Errorf("it was unable to retrieve the hostname, specify the connection name with --name YOUR_NAME")
	}
	return
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
		req.Command = []string{"psql", "-v", "ON_ERROR_STOP=1", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
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
		req.Subtype = proto.ConnectionTypeMongoDB.String()
		req.Command = []string{"mongo", "--quiet", "mongodb://$USER:$PASS@$HOST:$PORT/$DB?$OPTIONS"}
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

func parseHostEnvs() map[string]string {
	envs := map[string]string{}
	for _, envKey := range strings.Split(runFlags.EnvExport, ",") {
		if envKey == "" {
			continue
		}
		if val, exists := os.LookupEnv(envKey); exists {
			envs[fmt.Sprintf("envvar:%s", envKey)] = encb64(val)
		}
	}
	if len(envs) == 0 {
		for _, keyValEnv := range os.Environ() {
			key, val, found := strings.Cut(keyValEnv, "=")
			if !found {
				continue
			}
			envs[fmt.Sprintf("envvar:%s", key)] = encb64(val)
		}
	}
	return envs
}

func parseDefaultShell() (shellPath string, err error) {
	if shellEnv := os.Getenv("SHELL"); shellEnv != "" {
		return shellEnv, nil
	}
	shellPath, err = exec.LookPath("bash")
	if errors.Is(err, exec.ErrNotFound) {
		shellPath, err = exec.LookPath("sh")
	}
	return
}

func parsePosixCmd(command string) ([]string, error) {
	if command == "" {
		return nil, nil
	}
	r := strings.NewReader(command)
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(r, "")
	if err != nil {
		return nil, err
	}
	if len(f.Stmts) == 0 || len(f.Stmts) > 1 {
		return nil, fmt.Errorf("fail parsing command, empty or multiple statements found")
	}
	callExpr, ok := f.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, errors.New("unable to coerce to CallExpr")
	}
	printer := syntax.NewPrinter()
	var output []string
	for _, word := range callExpr.Args {
		var out []byte
		buf := bytes.NewBuffer(out)
		if err := printer.Print(buf, word); err != nil {
			return nil, fmt.Errorf("failed parsing word / part: %v", err)
		}
		output = append(output, buf.String())
	}
	return output, err
}

func encb64(v any) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", v)))
}
