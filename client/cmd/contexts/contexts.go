// Package contexts implements `hoop context`: named hoop installations the
// CLI can switch between with one command.
package contexts

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/hoophq/hoop/client/cmd/versions"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/spf13/cobra"
)

var (
	apiURLFlag    string
	grpcURLFlag   string
	tlsCAFlag     string
	apiKeyFlag    string
	overwriteFlag bool
	skipSyncFlag  bool
	forceFlag     bool
)

// MainCmd is the parent `hoop context` command.
var MainCmd = &cobra.Command{
	Use:     "context",
	Aliases: []string{"contexts"},
	Short:   "Switch between hoop installations",
	Long: `Save hoop installations as named contexts and switch between them.

Each context keeps its API URL, gRPC URL, TLS CA and login token in
$HOME/.hoop/contexts/<name>.toml. ` + "`hoop context use <name>`" + ` writes the context
into $HOME/.hoop/config.toml and syncs the CLI version with that gateway.

  hoop context create dev --api-url https://dev.example.com
  hoop context use dev
  hoop login
  hoop context list`,
	SilenceUsage: true,
}

func init() {
	createCmd.Flags().StringVar(&apiURLFlag, "api-url", "", "The API URL of the installation. Without it, the context copies the current configuration")
	createCmd.Flags().StringVar(&grpcURLFlag, "grpc-url", "", "The gRPC URL of the installation (optional)")
	createCmd.Flags().StringVar(&tlsCAFlag, "tls-ca", "", "The path to the TLS certificate authority to use (optional)")
	createCmd.Flags().StringVar(&apiKeyFlag, "api-key", "", "An API key to store as the login token (optional)")
	createCmd.Flags().BoolVar(&overwriteFlag, "overwrite", false, "Replace a context that already exists")
	useCmd.Flags().BoolVar(&skipSyncFlag, "skip-sync", false, "Do not sync the CLI version with the gateway")
	useCmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite a current configuration that no context keeps")
	removeCmd.Flags().BoolVar(&forceFlag, "force", false, "Remove the context even if it is active")

	MainCmd.AddCommand(createCmd, listCmd, showCmd, useCmd, removeCmd)
}

func store() (*clientconfig.ContextStore, error) { return clientconfig.DefaultContextStore() }

var createCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Save an installation as a named context",
	Long: `Save an installation as a named context.

With --api-url, the context uses the given values; run ` + "`hoop context use NAME`" + `
and ` + "`hoop login`" + ` to log in to it. Without --api-url, the context copies the
current configuration ($HOME/.hoop/config.toml), login included.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := store()
		if err != nil {
			return err
		}
		ctx, err := buildContext(s, args[0])
		if err != nil {
			return err
		}
		if s.Exists(ctx.Name) && !overwriteFlag {
			return fmt.Errorf("context %s already exists; use --overwrite to replace it", ctx.Name)
		}
		if err := s.Save(ctx); err != nil {
			return err
		}
		fmt.Printf("Context %s saved (api_url=%s)\n", ctx.Name, ctx.ApiURL)
		if apiURLFlag == "" {
			// it is a copy of config.toml, so it is the context in use
			return s.SetActive(ctx.Name)
		}
		return nil
	},
}

func buildContext(s *clientconfig.ContextStore, name string) (*clientconfig.Context, error) {
	if err := clientconfig.ValidateContextName(name); err != nil {
		return nil, err
	}
	if apiURLFlag == "" {
		if grpcURLFlag != "" || tlsCAFlag != "" || apiKeyFlag != "" {
			return nil, fmt.Errorf("--grpc-url, --tls-ca and --api-key need --api-url")
		}
		current, err := s.ReadConfigFile()
		if err != nil {
			return nil, err
		}
		if current.ApiURL == "" {
			return nil, fmt.Errorf("no current configuration to copy; pass --api-url")
		}
		current.Name = name
		return current, nil
	}
	apiURL := strings.TrimSuffix(apiURLFlag, "/")
	if _, err := grpc.ParseServerAddress(apiURL); err != nil {
		return nil, fmt.Errorf("--api-url value is not an http address")
	}
	if grpcURLFlag != "" {
		if _, err := grpc.ParseServerAddress(grpcURLFlag); err != nil {
			return nil, fmt.Errorf("--grpc-url value is not a gRPC address")
		}
	}
	ctx := &clientconfig.Context{Name: name, ApiURL: apiURL, GrpcURL: grpcURLFlag, Token: apiKeyFlag}
	if apiKeyFlag != "" && !strings.HasPrefix(apiKeyFlag, "hpk_") {
		return nil, fmt.Errorf("--api-key value must have 'hpk_' prefix")
	}
	if tlsCAFlag != "" {
		data, err := os.ReadFile(tlsCAFlag)
		if err != nil {
			return nil, fmt.Errorf("failed reading --tls-ca file: %v", err)
		}
		ctx.TlsCAB64Enc = base64.StdEncoding.EncodeToString(data)
	}
	return ctx, nil
}

var listCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List the saved contexts",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		s, err := store()
		if err != nil {
			return err
		}
		items, err := s.List()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No contexts yet. Try `hoop context create NAME --api-url URL`.")
			return nil
		}
		active, _ := s.Active()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, " \tNAME\tAPI URL\tLOGGED IN")
		for _, ctx := range items {
			mark := ""
			if ctx.Name == active {
				mark = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", mark, ctx.Name, ctx.ApiURL, yesNo(ctx.Token != ""))
		}
		return w.Flush()
	},
}

var showCmd = &cobra.Command{
	Use:          "show [NAME]",
	Short:        "Show a context (the active one by default)",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := store()
		if err != nil {
			return err
		}
		active, err := s.Active()
		if err != nil {
			return err
		}
		name := active
		if len(args) == 1 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("no active context; pass a NAME or run `hoop context use NAME`")
		}
		ctx, err := s.Get(name)
		if err != nil {
			return err
		}
		// the active context's login lives in config.toml until the next switch
		if name == active {
			if current, err := s.ReadConfigFile(); err == nil {
				current.Name = name
				ctx = current
			}
		}
		fmt.Printf("name=%s\n", ctx.Name)
		fmt.Printf("active=%s\n", yesNo(name == active))
		fmt.Printf("api_url=%s\n", ctx.ApiURL)
		fmt.Printf("grpc_url=%s\n", ctx.GrpcURL)
		fmt.Printf("logged_in=%s\n", yesNo(ctx.Token != ""))
		fmt.Println("token=OMITTED")
		if ctx.TlsCAB64Enc != "" {
			fmt.Printf("tls_ca=%s\n", ctx.TlsCAB64Enc)
		}
		return nil
	},
}

var useCmd = &cobra.Command{
	Use:   "use NAME",
	Short: "Switch the CLI to a context and sync its version with the gateway",
	Long: `Write the context into $HOME/.hoop/config.toml and make it active.

The login of the context you leave is kept in that context first. When the
target context has a login, the CLI then installs and activates the version
its gateway runs, as ` + "`hoop versions sync`" + ` does.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := store()
		if err != nil {
			return err
		}
		target, err := s.Get(args[0])
		if err != nil {
			return err
		}
		gate := checkFlag(target)
		if gate.err != nil {
			return gate.err
		}
		if _, err := s.Use(target.Name, forceFlag); err != nil {
			if errors.Is(err, clientconfig.ErrUnsavedConfig) {
				return fmt.Errorf("%v; save it first with `hoop context create NAME`, or pass --force to overwrite it", err)
			}
			return err
		}
		fmt.Printf("Switched to context %s (api_url=%s)\n", target.Name, target.ApiURL)
		switch {
		case skipSyncFlag:
			return nil
		case gate.syncHint != "":
			fmt.Println(gate.syncHint)
			return nil
		}
		err = versions.SyncIf(hasContextCommand)
		var notActivated *versions.NotActivatedError
		if errors.As(err, &notActivated) {
			fmt.Printf("The gateway runs %s, but hoop %s has no `hoop context`, so this CLI stays active to let you switch again.\n"+
				"Run `hoop versions use %s` to use that version anyway.\n", notActivated.Version, notActivated.Version, notActivated.Version)
			return nil
		}
		if err != nil {
			return fmt.Errorf("switched to context %s, but the version sync failed: %w", target.Name, err)
		}
		return nil
	},
}

// hasContextCommand refuses a CLI binary without `hoop context`: activating
// it would leave the user unable to switch contexts again. Released versions
// before this command answer `context --help` with "unknown command".
// `--help` runs no command code and reads no configuration.
func hasContextCommand(binary string) error {
	out, err := exec.Command(binary, "context", "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("it has no `hoop context` command (%v: %s)", err, strings.TrimSpace(firstLine(out)))
	}
	return nil
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	return line
}

type flagGate struct {
	// err refuses the switch.
	err error
	// syncHint, when set, skips the version sync and tells the user why.
	syncHint string
}

// checkFlag reads experimental.cli_contexts from the target gateway with the
// target's own login, because each gateway holds the flag for its own org.
func checkFlag(target *clientconfig.Context) flagGate {
	if target.Token == "" {
		return decideFlag(target.Name, nil, errNoLogin)
	}
	flags, err := clientconfig.FetchFeatureFlags(target.Config())
	return decideFlag(target.Name, flags, err)
}

var errNoLogin = errors.New("no login")

// decideFlag turns the target gateway's answer into a decision:
//   - flag reported off: refuse the switch;
//   - flag absent (a gateway that predates it): switch and sync;
//   - no login, or the gateway cannot answer (an expired login): switch, but
//     skip the sync, so the user can still reach the context to log in.
func decideFlag(name string, flags map[string]bool, err error) flagGate {
	if err != nil {
		reason := "the gateway did not answer"
		if errors.Is(err, errNoLogin) {
			reason = "the context has no login"
		}
		return flagGate{syncHint: fmt.Sprintf("Version sync skipped: %s. Run `hoop login`, then `hoop versions sync`.", reason)}
	}
	if enabled, ok := flags[featureflag.FlagCLIContexts]; ok && !enabled {
		return flagGate{err: fmt.Errorf("the gateway of context %s turned off %s; ask an admin to turn it on at Settings > Experimental",
			name, featureflag.FlagCLIContexts)}
	}
	return flagGate{}
}

var removeCmd = &cobra.Command{
	Use:          "remove NAME",
	Aliases:      []string{"rm"},
	Short:        "Delete a context",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := store()
		if err != nil {
			return err
		}
		active, err := s.Active()
		if err != nil {
			return err
		}
		if args[0] == active && !forceFlag {
			return fmt.Errorf("context %s is active; use --force to remove it ($HOME/.hoop/config.toml stays as it is)", args[0])
		}
		if err := s.Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("Context %s removed\n", args[0])
		return nil
	},
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
