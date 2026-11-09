// Package contexts implements `hoop context`: named hoop installations the
// CLI can switch between with one command.
package contexts

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

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

// lockedStore returns the store with its lock held, for commands that write it.
func lockedStore() (*clientconfig.ContextStore, func(), error) {
	s, err := store()
	if err != nil {
		return nil, nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, nil, err
	}
	return s, unlock, nil
}

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
		s, unlock, err := lockedStore()
		if err != nil {
			return err
		}
		defer unlock()
		ctx, err := buildContext(s, args[0])
		if err != nil {
			return err
		}
		if s.Exists(ctx.Name) && !overwriteFlag {
			return fmt.Errorf("context %s already exists; use --overwrite to replace it", ctx.Name)
		}
		// config.toml holds the active context's login and is saved back over
		// its file at the next switch, so replacing that file would be undone.
		active, err := s.Active()
		if err != nil {
			return err
		}
		if apiURLFlag != "" && ctx.Name == active {
			return fmt.Errorf("context %s is active; switch to another context before you replace it, or change it with `hoop login`", ctx.Name)
		}
		if err := s.Save(ctx); err != nil {
			return err
		}
		fmt.Printf("Context %s saved (api_url=%s)\n", ctx.Name, ctx.ApiURL)
		if apiURLFlag == "" {
			// a copy of config.toml is the context in use; keep the login of the one it replaces
			if err := s.SaveBack(); err != nil {
				return err
			}
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
	if _, err := grpc.ParseServerAddress(apiURL); err != nil ||
		!(strings.HasPrefix(apiURL, "http://") || strings.HasPrefix(apiURL, "https://")) {
		return nil, fmt.Errorf("--api-url value is not an http(s) address")
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
		active, err := s.Active()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, " \tNAME\tAPI URL\tLOGGED IN")
		for _, ctx := range items {
			mark := ""
			if ctx.Name == active {
				mark = "*"
				if ctx, err = s.Resolve(ctx.Name); err != nil {
					return err
				}
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
		// the active context's login lives in config.toml until the next switch
		ctx, err := s.Resolve(name)
		if err != nil {
			return err
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
its gateway runs, as ` + "`hoop versions sync`" + ` does.

A context with a login needs the flag experimental.cli_contexts on at its
gateway (Settings > Experimental). It is off by default.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		target, gate, err := switchContext(args[0])
		if err != nil {
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
		// The gate passed, so the gateway runs a release that has `hoop context`.
		// Sync against the context, not the HOOP_* env vars that Load prefers.
		if err := versions.Sync(target.Config()); err != nil {
			return fmt.Errorf("switched to context %s, but the version sync failed: %w", target.Name, err)
		}
		return nil
	},
}

// switchContext checks the flag and writes the target into config.toml, holding
// the store's lock so no other hoop process writes in between.
func switchContext(name string) (*clientconfig.Context, flagGate, error) {
	s, unlock, err := lockedStore()
	if err != nil {
		return nil, flagGate{}, err
	}
	defer unlock()
	// use the active context's latest login, not the one saved at the last switch
	target, err := s.Resolve(name)
	if err != nil {
		return nil, flagGate{}, err
	}
	gate := checkFlag(target)
	if gate.err != nil {
		return nil, gate, gate.err
	}
	if target, err = s.Use(target.Name, forceFlag); err != nil {
		if errors.Is(err, clientconfig.ErrUnsavedConfig) {
			err = fmt.Errorf("%v; save it first with `hoop context create NAME`, or pass --force to overwrite it", err)
		}
		return nil, gate, err
	}
	return target, gate, nil
}

type flagGate struct {
	// err refuses the switch.
	err error
	// syncHint, when set, skips the version sync and tells the user why.
	syncHint string
}

// checkFlag reads experimental.cli_contexts from the target gateway with the
// target's login: each gateway holds the flag for its own org.
func checkFlag(target *clientconfig.Context) flagGate {
	if target.Token == "" {
		return decideFlag(target.Name, nil, errNoLogin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), flagTimeout)
	defer cancel()
	flags, err := clientconfig.FetchFeatureFlagsContext(ctx, target.Config())
	return decideFlag(target.Name, flags, err)
}

// flagTimeout bounds the flag read, so a gateway that never answers cannot block the CLI.
const flagTimeout = 15 * time.Second

var errNoLogin = errors.New("no login")

// decideFlag allows the switch only when the gateway reports the flag on. Without
// a valid login there is no org to ask: switch, skip the sync, let the user log in.
func decideFlag(name string, flags map[string]bool, err error) flagGate {
	var status *clientconfig.StatusError
	switch {
	case errors.Is(err, errNoLogin),
		errors.As(err, &status) && (status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden):
		return flagGate{syncHint: fmt.Sprintf("Version sync skipped: the context has no valid login. "+
			"Run `hoop login`, then `hoop context use %s` again to sync the CLI version.", name)}
	case err != nil:
		return flagGate{err: fmt.Errorf("cannot read %s from the gateway of context %s: %v", featureflag.FlagCLIContexts, name, err)}
	}
	enabled, known := flags[featureflag.FlagCLIContexts]
	if !known {
		return flagGate{err: fmt.Errorf("the gateway of context %s is too old for %s; upgrade it, then ask an admin to turn the flag on at Settings > Experimental",
			name, featureflag.FlagCLIContexts)}
	}
	if !enabled {
		return flagGate{err: fmt.Errorf("%s is off on the gateway of context %s; ask an admin to turn it on at Settings > Experimental",
			featureflag.FlagCLIContexts, name)}
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
		s, unlock, err := lockedStore()
		if err != nil {
			return err
		}
		defer unlock()
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
