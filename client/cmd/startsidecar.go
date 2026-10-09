package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/client/cmd/sidecartui"
	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/version"
	// Analyzer providers register themselves on import, matching the
	// standalone hoop-inspect binary. Linking all three keeps the
	// config-decides-everything rule: turning on Vertex must not require a
	// different binary.
	"github.com/hoophq/hoop/sidecar/analytics"
	_ "github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	_ "github.com/hoophq/hoop/sidecar/analyzer/openai"
	_ "github.com/hoophq/hoop/sidecar/analyzer/vertex"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	// The gs:// descriptor fetcher, same rule as the providers above.
	_ "github.com/hoophq/hoop/sidecar/descriptors/gcs"
	"github.com/hoophq/hoop/sidecar/license"
	// The MCP server an "mcp" block turns on (ADR-0021), same rule again.
	_ "github.com/hoophq/hoop/sidecar/mcp"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// deprecatedSidecarAlias is the pre-rename name of this command. Cobra routes
// it to the same command, and the RunE prints a notice pointing at the new one.
const deprecatedSidecarAlias = "inspect"

var (
	sidecarConfigFlag     string
	sidecarLicenseFlag    string
	sidecarTokenFlag      string
	sidecarValidateFlag   bool
	sidecarStrictFlag     bool
	sidecarMigrateFlag    bool
	sidecarMigrateOutFlag string
	sidecarLogFormatFlag  string
)

var startSidecarCmd = &cobra.Command{
	Use:     "sidecar",
	Aliases: []string{deprecatedSidecarAlias},
	Short:   "Runs the inspection sidecar",
	Long: `Runs the hoop-inspect relay: an inspecting proxy that decodes the wire
protocol between a client and a database or API, evaluates each statement
against policy, records an audit trail, and masks sensitive values on the way
back.

It routes nothing and terminates no DOWNSTREAM TLS. Run it behind something
that already owns the network path and identity, typically an Envoy sidecar
forwarding plaintext over loopback or a unix socket. The hop to the backend
can be TLS (upstream_tls): the relay originates it and still inspects, since
it is the client on that hop and decrypts what it reads.

Every capability is decided by the config file, so turning on PII detection
does not require a different binary. The file may be YAML or JSON; the
extension picks the parser.

Run with nothing at all — no config, no flags — and a built-in default
starts instead: one loopback URL that forwards to the getting-started
guide. It inspects no traffic; it exists so the first run after the
install works. Write a config to replace it. On an interactive terminal it
draws a screen instead of the banner, and pressing w there sets up a config:
pick the demo (an invented API the CLI serves) or a protocol, say where the
backend is, adjust the default guardrail, masking, analyzer and detection,
then save it, or save and boot the sidecar on it without restarting.

This command was named "inspect". That name still works as a deprecated
alias.

The config schema changed in ADR-0011: "policy" split into "guardrails" and
"opa", and three fields were dropped. Both spellings load, and the old one
prints a warning naming its replacement. Use --strict to fail on one.

Without a license the process caps guardrail and data masking rules at one
each and says so at startup. A license lifts the caps for the features it
names. It may be a path or the document itself. A sidecar connected to a
control plane that manages licensing runs under the plane's license only,
and every local source is ignored with a warning. Otherwise --license
outranks HOOP_LICENSE, which outranks the "license" key in the config file.

A sidecar may connect to a Control Plane instead of carrying its own
listeners: set HOOP_CONTROL_PLANE_URL or the "control_plane_url" config key
(the env var outranks the key), and pass the token from the sidecar's
registration with --token, which outranks HOOP_SIDECAR_TOKEN. The handshake
then supplies the whole running config and --config becomes optional. A
Control Plane holding no configuration is seeded with the config file's
document on that first handshake, so an existing sidecar connects by adding
the URL and passing the token, nothing else; once the plane holds a config
it owns it, and listeners still in the file are ignored with a warning. The
token is shown once when the sidecar is created; a lost one means
registering a new sidecar. A sidecar whose control plane entry says
"load_from_disk" runs the listeners in its own config file and receives only
its license; moving that entry either way reaches a running sidecar on its
next check-in, which applies what a live process can change and logs what
needs a restart.`,
	Example: `  hoop start sidecar
  hoop start sidecar --config /etc/hoop-inspect/config.yaml
  hoop start sidecar --config config.yaml --license /etc/hoop-inspect/license.json
  hoop start sidecar --config config.yaml --validate
  hoop start sidecar --config config.yaml --validate --strict
  HOOP_CONTROL_PLANE_URL=https://cp.example.com hoop start sidecar --token hsc_...`,
	// A bad config is not a usage error, and dumping the flag list under one
	// buries the message that says which field is wrong.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		warnDeprecatedSidecarAlias(os.Stderr, cmd.CalledAs())

		// Read before anything starts, so a typo is a usage error and not a
		// sidecar that came up in a format nobody asked for.
		logFormat, err := sidecartui.ParseFormat(sidecarLogFormatFlag)
		if err != nil {
			cmd.SilenceUsage = false
			return err
		}

		// Resolved before Setup: in the TUI the person at the terminal
		// reviews held statements, which is what lets a sidecar with no
		// control plane load require_review at all. Everywhere else
		// (a pipe, CI, a container) nobody could answer, and Setup keeps
		// refusing such a config.
		stdoutTTY, stdinTTY := term.IsTerminal(int(os.Stdout.Fd())), term.IsTerminal(int(os.Stdin.Fd()))
		format := sidecartui.Resolve(logFormat, stdoutTTY, stdinTTY, os.Getenv)

		if sidecarConfigFlag == "" && os.Getenv(daemon.ControlPlaneURLEnv) == "" {
			if !sidecarBareInvocation(cmd, args) {
				// The one genuine usage error here, so let cobra show the flags.
				cmd.SilenceUsage = false
				return fmt.Errorf("--config is required (or set HOOP_SIDECAR_CONFIG or %s)",
					daemon.ControlPlaneURLEnv)
			}
			firstRunOpts := []daemon.Option{
				daemon.WithEntrypoint(analytics.EntrypointCLI),
				daemon.WithDeprecatedAlias(cmd.CalledAs() == deprecatedSidecarAlias),
			}
			// A pipe, CI or NO_COLOR keeps the prose banner.
			if format != sidecartui.FormatTUI || !sidecartui.Interactive(stdoutTTY, stdinTTY) {
				return daemon.FirstRun(os.Stdout, "hoop start sidecar --config config.yaml", firstRunOpts...)
			}
			// A person at a terminal gets the first-run screen, where they
			// can set up a config and boot it without restarting.
			boot, err := sidecartui.RunFirstRun(sidecartui.FirstRunOptions{
				Version:      daemon.Version,
				Validate:     validateSidecarConfig,
				ConnectCheck: checkControlPlane,
				OpenURL:      openBrowser,
				LicenseDir:   hoopLicenseDir(),
				// A license saved on the Connect page is this run's
				// --license: what is validated and booted next uses it.
				UseLicense: func(path string) { sidecarLicenseFlag = path },
			}, func(ctx context.Context, obs daemon.FirstRunObserver) error {
				return daemon.FirstRunContext(ctx, io.Discard, "hoop start sidecar --config "+configyaml.StarterFile,
					append(firstRunOpts, daemon.WithFirstRunObserver(obs))...)
			})
			if err != nil || boot == nil {
				return err
			}
			// Booted from the first-run screen: from here on this is the
			// run `hoop start sidecar --config <file>` would have been, or,
			// connected to a Control Plane, the run with its URL and token.
			// Both may be set: a plane with no config yet takes the file's on
			// this first handshake, and manages it from then on.
			if boot.ControlPlaneURL != "" {
				if err := os.Setenv(daemon.ControlPlaneURLEnv, boot.ControlPlaneURL); err != nil {
					return err
				}
				sidecarTokenFlag = boot.Token
			}
			if boot.ConfigPath != "" {
				if err := refusePlainPlane(boot.ConfigPath); err != nil {
					return err
				}
			}
			sidecarConfigFlag = boot.ConfigPath
		}
		if sidecarMigrateFlag {
			if sidecarConfigFlag == "" {
				cmd.SilenceUsage = false
				return fmt.Errorf("--migrate needs --config: it rewrites a file, " +
					"and a control-plane config has no file to rewrite")
			}
			cfg, err := configyaml.Load(sidecarConfigFlag)
			if err != nil {
				return err
			}
			out := os.Stdout
			if sidecarMigrateOutFlag != "" {
				f, err := os.Create(sidecarMigrateOutFlag)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}
			// The destination's extension picks the syntax; stdout
			// inherits the input's.
			target := sidecarMigrateOutFlag
			if target == "" {
				target = sidecarConfigFlag
			}
			return daemon.WriteMigrated(cfg, configyaml.IsYAML(target), out, os.Stderr)
		}

		setupOpts := []daemon.Option{
			daemon.WithLicense(sidecarLicenseFlag),
			daemon.WithControlPlaneToken(sidecarTokenFlag),
			daemon.WithEntrypoint(analytics.EntrypointCLI),
			daemon.WithDeprecatedAlias(cmd.CalledAs() == deprecatedSidecarAlias),
		}
		var reviewer *sidecartui.Reviewer
		if format == sidecartui.FormatTUI && sidecartui.Interactive(stdoutTTY, stdinTTY) {
			reviewer = sidecartui.NewReviewer()
			setupOpts = append(setupOpts, daemon.WithLocalReviewer(reviewer.For))
		}
		cfg, det, err := daemon.SetupWith(sidecarConfigFlag, configyaml.Load, buildSidecarPlugin, setupOpts...)
		if err != nil {
			return err
		}

		// Same channel as the rename notice above, and for the same reason:
		// --validate writes a parseable report to stdout.
		daemon.ReportDeprecations(os.Stderr, cfg.Deprecations)
		if sidecarStrictFlag && len(cfg.Deprecations) > 0 {
			return fmt.Errorf("%d deprecated config field(s) in use and --strict is set",
				len(cfg.Deprecations))
		}

		if sidecarValidateFlag {
			lanes, err := daemon.Validate(cfg, det)
			if err != nil {
				return err
			}
			return daemon.PrintLanes(os.Stdout, cfg.Licensing(), lanes)
		}

		// A config written by the setup screen's demo names the demo API
		// as its upstream; the CLI serves it for as long as the sidecar
		// runs, so the demo config works on every boot, not only the first.
		var notes []string
		var demo *sidecartui.DemoOptions
		running := &sidecarDemo{stop: func() {}}
		if sidecarConfigFlag != "" {
			running, err = startSidecarDemo(sidecarConfigFlag, cfg)
			if err != nil {
				return err
			}
			defer running.stop()
			notes = running.notes
			if running.ports != nil {
				demo = &sidecartui.DemoOptions{OpenURL: openBrowser, Ports: *running.ports}
			}
		}

		// Run blocks until SIGINT or SIGTERM and installs its own handler.
		// The format only changes how its output reaches the terminal: a
		// pipe, a file, a container or CI keeps the JSON it always wrote.
		// Connected to a control plane, approvals are decided there; the
		// dashboard's Approvals section links to them instead.
		plane, _, _ := cfg.ControlPlane()
		return sidecartui.Run(format, sidecartui.Options{
			Version:      daemon.Version,
			AuditFile:    cfg.Audit.File,
			Reviewer:     reviewer,
			Operator:     sidecarOperator(),
			SaveDir:      sidecarSaveDir(),
			Notes:        notes,
			Demo:         demo,
			ControlPlane: plane,
			OpenURL:      openBrowser,
		}, func() error { return errors.Join(daemon.Run(cfg, det), running.err()) })
	},
}

// startSidecarDemo serves the demo API when the config at path carries
// configyaml.DemoAPIKey, and returns what stops it. A config without the key
// starts nothing. The address must be loopback: the demo API answers anyone
// who reaches it, and invented data is still not something to expose.
func startSidecarDemo(path string, cfg *daemon.Config) (*sidecarDemo, error) {
	none := &sidecarDemo{stop: func() {}}
	// The setup screens write the demo key only into YAML. A JSON config
	// is not parsed as YAML: valid JSON (a \/ escape, a repeated key) can
	// fail that parse, and would then stop a boot that works without it.
	if !configyaml.IsYAML(path) {
		return none, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return none, err
	}
	addr, ok, err := configyaml.ExtensionValue(data, configyaml.DemoAPIKey)
	if err != nil || !ok {
		return none, err
	}
	bind, err := loopbackBind(addr)
	if err != nil {
		return none, fmt.Errorf("%s: %w", configyaml.DemoAPIKey, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc, err := sidecardemo.Serve(ctx, bind)
	if err != nil {
		cancel()
		return none, err
	}
	d := &sidecarDemo{stop: cancel}
	go d.watch(errc)
	// The tour sends its requests through the listener in front of the
	// API. Without one, there is nothing to tour: a default port would
	// reach another service, or nothing at all.
	for _, l := range cfg.Listeners {
		// Listen goes into the curl commands shown and copied: only a tcp
		// loopback address, never a socket path a config could fill with
		// shell syntax.
		if l.Network != "" && l.Network != "tcp" {
			continue
		}
		if _, err := loopbackBind(l.Listen); err != nil {
			continue
		}
		if l.Upstream == addr {
			d.ports = &sidecardemo.Ports{API: addr, Listen: l.Listen}
			break
		}
	}
	if d.ports == nil {
		d.notes = []string{"demo API served at " + addr + ", but no listener forwards to it: " +
			"set a listener's upstream to " + addr + " to try the demo"}
		return d, nil
	}
	d.notes = []string{"demo API served at " + addr + "; try, from another terminal:"}
	for _, c := range sidecardemo.TryCommands(*d.ports) {
		d.notes = append(d.notes, "  "+c)
	}
	return d, nil
}

// sidecarDemo is the demo API running beside the sidecar.
type sidecarDemo struct {
	stop func()
	// ports is where the tour sends requests, nil when no listener fronts
	// the API and the tour stays off.
	ports *sidecardemo.Ports
	notes []string

	mu     sync.Mutex
	failed error
}

// watch reports a demo API that stops serving while the sidecar runs. The
// log line reaches the dashboard's Logs and System; err() hands it to the
// run's result, so it is not lost when the screen closes.
func (d *sidecarDemo) watch(errc <-chan error) {
	err := <-errc
	if err == nil {
		return
	}
	d.mu.Lock()
	d.failed = err
	d.mu.Unlock()
	slog.Error("the demo API stopped serving; the demo listener has nothing to forward to", "error", err.Error())
}

func (d *sidecarDemo) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed == nil {
		return nil
	}
	return fmt.Errorf("the demo API stopped: %w", d.failed)
}

// loopbackBind is the address the demo API binds for addr, which must be
// this machine's: a literal loopback IP, or localhost bound as 127.0.0.1
// rather than whatever the resolver maps the name to.
func loopbackBind(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "localhost" {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%s is not a loopback address; the demo API only serves this machine", addr)
	}
	return addr, nil
}

// warnDeprecatedSidecarAlias renders the rename notice to w when the command
// was reached through the old name. calledAs is the token the user typed, so
// the notice stays silent for the new name.
//
// It goes to stderr: --validate writes a report to stdout that an operator may
// parse, and a warning must not land in it.
func warnDeprecatedSidecarAlias(w io.Writer, calledAs string) {
	if calledAs != deprecatedSidecarAlias {
		return
	}
	msg := styles.ClientErrorSimple(fmt.Sprintf(
		"warn: \"hoop start %s\" is deprecated and aliases to \"hoop start sidecar\".\n"+
			"Use \"hoop start sidecar\"; the alias is removed in a future release.",
		deprecatedSidecarAlias))
	_, _ = fmt.Fprintf(w, "%s\n", msg)
}

// sidecarBareInvocation reports whether this invocation asked for nothing:
// no config anywhere, no control plane, no flag, no argument. That is the
// one case that runs the first-run default (daemon.FirstRun) — a loopback
// URL forwarding to the getting-started guide — instead of a usage error,
// so a user's first contact after the install is a working URL.
//
// The gate keys on what the user typed, not on resulting values: a flag
// set to its default (--validate=false, --license=) or an inherited global
// flag (--debug) still keeps the error, because typing anything without a
// config is a mistake to report, not a request for the demo.
//
// The caller has already established that sidecarConfigFlag (whose default
// comes from the environment) and the control plane env var are empty.
func sidecarBareInvocation(cmd *cobra.Command, args []string) bool {
	if len(args) > 0 {
		return false
	}
	changed := false
	seen := func(f *pflag.Flag) { changed = changed || f.Changed }
	cmd.Flags().VisitAll(seen)
	cmd.InheritedFlags().VisitAll(seen)
	return !changed
}

// checkControlPlane connects to a Control Plane the way the boot that
// follows the Connect page will: the same Setup, so the handshake runs and
// a wrong token or an unreachable plane is reported on the page, then the
// same Validate over the config the plane serves. The URL is set for the
// call only; the boot sets it for good.
func checkControlPlane(planeURL, token string) (*daemon.Config, error) {
	prev, had := os.LookupEnv(daemon.ControlPlaneURLEnv)
	if err := os.Setenv(daemon.ControlPlaneURLEnv, planeURL); err != nil {
		return nil, err
	}
	defer func() {
		if had {
			_ = os.Setenv(daemon.ControlPlaneURLEnv, prev)
		} else {
			_ = os.Unsetenv(daemon.ControlPlaneURLEnv)
		}
	}()
	cfg, det, err := daemon.SetupWith("", configyaml.Load, buildSidecarPlugin,
		daemon.WithControlPlaneToken(token),
		daemon.WithEntrypoint(analytics.EntrypointCLI),
		daemon.WithLocalReviewer(sidecartui.NewReviewer().For))
	if err != nil {
		return nil, err
	}
	if _, err := daemon.Validate(cfg, det); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateSidecarConfig checks a config the way the boot that follows the
// setup screen loads it: Setup resolves the license from --license,
// HOOP_LICENSE or the file, so the rule caps are the ones that will apply,
// and a terminal reviewer is attached, as the dashboard attaches one, so
// require_review validates. Then the same Validate --validate runs.
func validateSidecarConfig(path string) (string, error) {
	if err := refusePlainPlane(path); err != nil {
		return "", err
	}
	cfg, det, err := daemon.SetupWith(path, configyaml.Load, buildSidecarPlugin,
		daemon.WithLicense(sidecarLicenseFlag),
		daemon.WithEntrypoint(analytics.EntrypointCLI),
		daemon.WithLocalReviewer(sidecartui.NewReviewer().For))
	if err != nil {
		return "", err
	}
	lanes, err := daemon.Validate(cfg, det)
	if err != nil {
		return "", err
	}
	noun := "listeners"
	if len(lanes) == 1 {
		noun = "listener"
	}
	return fmt.Sprintf("%d %s · %s", len(lanes), noun,
		strings.TrimPrefix(daemon.LimitsSummary(cfg.Licensing()), "limits: ")), nil
}

// refusePlainPlane refuses a file whose control_plane_url would send the
// sidecar token in the clear. The first-run screen lists every config in
// the folder, and checking one runs the handshake: a file in a cloned
// repository must not take a token exported in this shell.
func refusePlainPlane(path string) error {
	cfg, err := configyaml.Load(path)
	if err != nil || cfg.ControlPlaneURL == "" {
		return nil // SetupWith reports a file it cannot load
	}
	if err := sidecartui.PlaneTransportError(cfg.ControlPlaneURL); err != nil {
		return fmt.Errorf("%s: control_plane_url: %w", path, err)
	}
	return nil
}

// sidecarOperator names the person reviewing at this terminal: the OS
// account that started the process, which is who the decision is recorded
// against.
func sidecarOperator() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// sidecarSaveDir is where the TUI keeps a copy of what it shows: the hoop
// directory the CLI already uses for its own files. Empty when the home
// directory is unknown; the TUI then says nothing is saved.
func sidecarSaveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoop", "sidecar")
}

// hoopLicenseDir is where a license entered on the first-run screen is
// kept: ~/.hoop/license. Not under sidecar/, because a hoop license is the
// organization's, not this sidecar's. Empty when the home directory is
// unknown; the screen then says it has nowhere to save one.
func hoopLicenseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoop", "license")
}

// sidecarConfigFromEnv reads the config path from the environment. It prefers
// the current name and falls back to the pre-rename one, so a deployment that
// still sets HOOP_INSPECT_CONFIG keeps working.
func sidecarConfigFromEnv() string {
	if v := os.Getenv("HOOP_SIDECAR_CONFIG"); v != "" {
		return v
	}
	return os.Getenv("HOOP_INSPECT_CONFIG")
}

// buildSidecarPlugin constructs the PII detector from the config's "pii"
// section.
//
// An absent section no longer disables detection: the plugin builds a
// detector over every entity type it knows, and the section narrows it. A nil
// Plugin means only that a build linked no detector, which this one does not.
//
// The conversion still matters. A nil alcatraz.Plugin converts to a nil
// daemon.Plugin rather than to a non-nil interface holding a nil pointer,
// which the sidecar would call through.
func buildSidecarPlugin(raw json.RawMessage) (daemon.Plugin, error) {
	return alcatraz.PluginFromConfig(raw)
}

func init() {
	// The sidecar reports this at /stats, in its startup log and in the
	// User-Agent of its outbound calls, so an operator reading any of them
	// sees the hoop version that produced the binary rather than "unknown".
	daemon.Version = version.Get().Version
	// --migrate renders YAML through the same module that parses it; the
	// daemon package cannot import it, so the renderer is injected.
	daemon.YAMLFromJSON = configyaml.FromJSON

	startSidecarCmd.Flags().StringVar(&sidecarConfigFlag, "config", sidecarConfigFromEnv(),
		"Path to the inspection config file (YAML or JSON)")
	// No default from the environment, unlike --config. daemon.Setup reads
	// HOOP_LICENSE when this is empty, which holds the precedence in one
	// place and keeps a customer's license out of --help.
	startSidecarCmd.Flags().StringVar(&sidecarLicenseFlag, "license", "",
		"Path to the license file, or the license document itself. Overrides "+
			license.EnvVar+" and the config file's \"license\" key")
	// Same rule as --license: no default from the environment, so daemon
	// setup holds the precedence in one place and the token stays out of
	// --help output.
	startSidecarCmd.Flags().StringVar(&sidecarTokenFlag, "token", "",
		"The token identifying this sidecar to the control plane. Overrides "+
			daemon.SidecarTokenEnv+". Exclusive with "+daemon.SidecarIdentityTypeEnv)
	startSidecarCmd.Flags().BoolVar(&sidecarValidateFlag, "validate", false,
		"Validate the config, report what each listener resolved to, and exit")
	startSidecarCmd.Flags().BoolVar(&sidecarStrictFlag, "strict", false,
		"Fail when the config uses a deprecated field, so a pipeline can catch it "+
			"before the release that removes it")
	startSidecarCmd.Flags().BoolVar(&sidecarMigrateFlag, "migrate", false,
		"Rewrite the config onto the current schema, print it, and exit. Deprecated "+
			"fields are folded and ai_analysis rules become listener analyzer blocks "+
			"where the move is faithful")
	startSidecarCmd.Flags().StringVar(&sidecarMigrateOutFlag, "migrate-out", "",
		"File --migrate writes to instead of stdout; its extension picks the syntax, "+
			"defaulting to the input's")

	startSidecarCmd.Flags().StringVar(&sidecarLogFormatFlag, "log-format", string(sidecartui.FormatAuto),
		"How output reaches the terminal: auto, tui, text or json. auto draws the TUI on an "+
			"interactive terminal and writes JSON to a pipe, a file or CI (text when NO_COLOR or TERM=dumb)")

	startCmd.AddCommand(startSidecarCmd)
}
