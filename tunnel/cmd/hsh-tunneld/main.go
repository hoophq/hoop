// Command hsh-tunneld is the Hoop Tunnel daemon. It brings up a userspace
// netstack + TUN device on the host, then routes every TCP connection to
// a *.hoop address through the existing hoop gateway as if it were a
// normal `hoop connect <name>` session.
//
// hsh-tunneld uses NO custom gateway protocol: each accepted TCP flow
// opens its own gRPC bidirectional stream to the gateway (the same one
// the `hoop` CLI uses). The gateway sees these as regular client
// sessions; auth, review, audit, DLP, access control, webhooks, and
// slack integrations all apply automatically.
//
// The daemon is meant to run as a system service (LaunchDaemon / systemd /
// Windows Service) and be driven by the unprivileged `hsh` CLI (from the
// hoophq/hsh repo) via local IPC. For development / manual testing it can
// also be run directly with environment variables.
//
// Linux usage (manual / dev):
//
//	# Build
//	go build ./tunnel/cmd/hsh-tunneld
//
//	# Configure (same envs as `hoop` CLI)
//	export HOOP_APIURL=http://127.0.0.1:8009
//	export HOOP_TOKEN=<your bearer token>
//	# HOOP_GRPCURL is optional: when omitted the gRPC address is fetched
//	# from GET /api/serverinfo automatically (same mechanism as hoop login).
//
//	# Run (requires CAP_NET_ADMIN; sudo is easiest for dev)
//	sudo -E ./hsh-tunneld
//
//	# Or grant the capability once and run unprivileged:
//	sudo setcap cap_net_admin+ep ./hsh-tunneld
//	./hsh-tunneld
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/resolver"
	"github.com/hoophq/hoop/tunnel/tunnelmgr"
)

// userAgent returns the User-Agent value the daemon presents on its
// outbound gRPC dials. It includes the build version so gateway-side
// audit logs can tell apart different daemon revisions.
func userAgent() string {
	v := version.Get().Version
	if v == "" {
		v = "unknown"
	}
	return fmt.Sprintf("hsh-tunneld/%s", v)
}

func main() {
	logger := log.New(os.Stderr, "hsh-tunneld ", log.LstdFlags|log.Lmicroseconds)

	// Verb dispatch. We do not pull cobra in for this because the verb
	// set is small and stable, and the daemon path needs to stay
	// flag.Parse-driven for backwards compatibility with the systemd
	// ExecStart and dev `sudo -E ./hsh-tunneld` invocations.
	//
	// Convention: a non-flag argv[1] (i.e. one not starting with '-')
	// is interpreted as a verb. Anything that starts with '-' falls
	// through to the daemon's flag.Parse so existing `--tld foo` style
	// invocations keep working exactly as they did.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		verb := os.Args[1]
		rest := os.Args[2:]
		if err := dispatchVerb(verb, rest, logger); err != nil {
			logger.Fatal(err)
		}
		return
	}

	if err := runDaemon(logger); err != nil {
		logger.Fatal(err)
	}
}

// dispatchVerb routes a non-flag first argument to one of the
// management verbs. Unknown verbs print the top-level usage block to
// stderr and exit non-zero so a typo isn't silently treated as "start
// the daemon with weird argv".
func dispatchVerb(verb string, args []string, logger *log.Logger) error {
	switch verb {
	case "install":
		return runInstall(args)
	case "uninstall":
		return runUninstall(args)
	case "validate-config":
		return runValidateConfig(args)
	case "status":
		return runServiceStatus(args)
	case "start":
		return runServiceStart(args)
	case "stop":
		return runServiceStop(args)
	case "version":
		return runVersion(args, logger)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", verb)
	}
}

// runDaemon is the legacy main path: parse the daemon flags, bring up
// the netstack + IPC, wait for SIGTERM. Extracted from the old main()
// so the verb dispatcher can sit alongside without ballooning main.
func runDaemon(logger *log.Logger) error {
	tld := flag.String("tld", resolver.DefaultTLD, "TLD owned by the tunnel (HSH_TUNNEL_DOMAIN overrides)")
	devName := flag.String("dev", "", "TUN device name (kernel picks if empty)")
	sessionSeed := flag.String("session", "spike-session", "session seed (controls the /48 prefix)")
	// ipcSocket and ipcTokenFile control the local control plane (RD-215).
	// Empty `--ipc-socket` skips the IPC layer entirely, which keeps the
	// daemon usable for standalone dev / integration tests where the
	// operator does not want to touch /var/run.
	ipcSocket := flag.String("ipc-socket", "", "path of the IPC unix socket (empty disables IPC; default in production: "+ipc.DefaultSocketPathUnix+")")
	ipcTokenFile := flag.String("ipc-token-file", "", "path of the control-token file (defaults to <dir-of-ipc-socket>/control-token)")
	ipcGroup := flag.String("ipc-group", "", "OS group that owns the IPC socket (members can connect; empty leaves it owned by the daemon's primary group)")
	// configFile is the daemon-managed TOML config: api_url, grpc_url,
	// token, log_level (RD-216). HSH_TUNNELD_CONFIG env var overrides.
	configFile := flag.String("config-file", "", "path of the daemon's TOML config (HSH_TUNNELD_CONFIG overrides; default: "+daemonconfig.DefaultConfigPathPlatform()+")")
	flag.Parse()

	if env := os.Getenv("HSH_TUNNEL_DOMAIN"); env != "" {
		*tld = env
	}
	if env := os.Getenv("HSH_TUNNELD_CONFIG"); env != "" && *configFile == "" {
		*configFile = env
	}
	if *configFile == "" {
		*configFile = daemonconfig.DefaultConfigPathPlatform()
	}

	cfg := runOptions{
		tld:          *tld,
		devName:      *devName,
		sessionSeed:  *sessionSeed,
		ipcSocket:    *ipcSocket,
		ipcTokenFile: *ipcTokenFile,
		ipcGroup:     *ipcGroup,
		configFile:   *configFile,
	}
	return run(logger, cfg)
}

// runOptions groups every configurable knob `run` cares about. Using a
// struct here keeps the function signature stable as we add more
// switches (login flags in RD-216, service-mode flags in RD-217).
type runOptions struct {
	tld         string
	devName     string
	sessionSeed string

	// IPC control-plane knobs. Empty ipcSocket disables IPC entirely.
	ipcSocket    string
	ipcTokenFile string
	ipcGroup     string

	// configFile is the path of the daemon-managed TOML config file.
	configFile string
}

func run(logger *log.Logger, opts runOptions) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Load the daemon-managed TOML config first. A missing file is the
	// "fresh install" case — Load returns the zero Config and we
	// proceed without a token. Env vars overlay any explicit values so
	// dev/integration runs can still drive everything via HOOP_*.
	fileCfg, err := daemonconfig.Load(opts.configFile)
	if err != nil {
		return fmt.Errorf("load config %q: %w", opts.configFile, err)
	}
	cfg := mergeConfigWithEnv(fileCfg)
	logger.Printf("config: file=%s api_url=%q logged_in=%v", opts.configFile, cfg.APIURL, cfg.LoggedIn())

	// Build the lifecycle owner. The Manager handles every operation
	// that touches the netstack / gateway / route table; main.go only
	// triggers it via the daemonService.
	mgr, err := tunnelmgr.New(tunnelmgr.Options{
		Logger:        logger,
		SessionSeed:   opts.sessionSeed,
		TLD:           opts.tld,
		DeviceName:    opts.devName,
		TLSSkipVerify: os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true",
		TLSServerName: os.Getenv("HOOP_TLSSERVERNAME"),
		UserAgent:     userAgent(),
	})
	if err != nil {
		return fmt.Errorf("init tunnelmgr: %w", err)
	}

	// Build the IPC-only service surface. /v1/status, /v1/login/start
	// etc. serve regardless of whether the netstack ever comes up;
	// login + config writes mutate this service's state and persist
	// back to opts.configFile. The service also drives Manager.BringUp
	// after a successful login (no daemon restart required).
	svc, err := newDaemonService(daemonServiceOptions{
		Manager:       mgr,
		ParentContext: ctx,
		ConfigPath:    opts.configFile,
		InitialConfig: cfg,
		OnTunnelUp: func(snap tunnelmgr.Snapshot) {
			printTunnelUpBanner(logger, snap, opts.tld)
		},
		OnTunnelDown: func() {
			logger.Printf("tunnel torn down — netstack and routes released")
		},
	})
	if err != nil {
		return fmt.Errorf("init service: %w", err)
	}

	// Start the IPC control plane. Returns a no-op shutdown closer
	// when --ipc-socket is empty (dev / integration) so the rest of
	// the function does not need to branch on that.
	shutdownIPC := startIPCServer(ctx, logger, opts, svc)
	defer shutdownIPC()

	// If we already have a token, bring the tunnel up immediately at
	// startup. A failure here is logged + recorded as lastError on
	// the service so /v1/status surfaces it; we DO NOT exit — the
	// operator may want to use `hsh tunnel logout` + `hsh tunnel
	// login` to recover without a restart.
	if cfg.LoggedIn() {
		if err := svc.BringUpFromConfig(); err != nil {
			logger.Printf("startup bring-up failed: %v", err)
			logger.Printf("  the IPC plane stays up; use `hsh tunnel logout` + `hsh tunnel login` to recover")
		}
	} else {
		logger.Printf("not logged in — netstack will not start.")
		logger.Printf("  run `hsh tunnel login` (the tunnel will come up automatically when you finish).")
	}

	// Block on signal. TearDown on shutdown is best-effort: the
	// per-tunnel ctx is parented to ctx, so closing it cancels the
	// gVisor goroutines anyway. We still call TearDown explicitly so
	// the routes get unconfigured (otherwise tun0 is left behind with
	// its /128 host address until the kernel reaps it).
	<-ctx.Done()
	logger.Printf("shutting down")
	if err := mgr.TearDown(); err != nil {
		logger.Printf("teardown: %v", err)
	}
	return nil
}

// mergeConfigWithEnv overlays HOOP_* env vars on top of the file-loaded
// config. Env wins so dev/integration runs can drive the daemon without
// touching the persisted config. We do NOT write the env-supplied
// values back to disk — the operator who set HOOP_TOKEN= in their
// shell did so deliberately; persisting would be a surprise.
func mergeConfigWithEnv(file daemonconfig.Config) daemonconfig.Config {
	out := file
	if v := os.Getenv("HOOP_APIURL"); v != "" {
		out.APIURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("HOOP_TOKEN"); v != "" {
		out.Token = v
	}
	if v := os.Getenv("HOOP_GRPCURL"); v != "" {
		out.GrpcURL = v
	}
	return out
}

// printTunnelUpBanner emits the multi-line "tunnel is up" log block
// the operator sees after either a startup bring-up or a successful
// hot-login. Kept separate from the lifecycle code so the banner can
// evolve without churning tunnelmgr.
func printTunnelUpBanner(logger *log.Logger, snap tunnelmgr.Snapshot, tld string) {
	logger.Printf("tunnel is up.")
	logger.Printf("  host addr: %s (%s)", snap.HostAddr, snap.DeviceName)
	logger.Printf("  resolver:  %s:53 (gVisor)", snap.Gateway)
	logger.Printf("")
	// Pick the first known connection name (sorted for stability) to
	// suggest a concrete `dig` invocation. Empty allocator → skip the
	// hint rather than print "@... .<tld>" with a blank name.
	if snap.Allocator != nil {
		names := snap.Allocator.Names()
		if len(names) > 0 {
			first := names[0]
			for _, n := range names[1:] {
				if n < first {
					first = n
				}
			}
			logger.Printf("  try:       dig @%s %s.%s AAAA", snap.Gateway, first, tld)
			logger.Printf("")
			logger.Printf("To route *.%s through this resolver host-wide (systemd-resolved):", tld)
			logger.Printf("  sudo resolvectl dns %s %s", snap.DeviceName, snap.Gateway)
			logger.Printf("  sudo resolvectl domain %s '~%s'", snap.DeviceName, tld)
			logger.Printf("After that:  psql -h %s.%s ...   (or any *.%s host)", first, tld, tld)
		}
	}
}

// startIPCServer brings up the local control plane if opts.ipcSocket
// is set, otherwise returns a no-op shutdown closer. It returns
// immediately after the listener is in place; serving runs in a
// background goroutine.
//
// Errors from the IPC layer are non-fatal: the daemon's primary job is
// the tunnel, and a degraded control plane is a configuration / perms
// issue we want the operator to see in the logs without losing data
// plane.
func startIPCServer(ctx context.Context, logger *log.Logger, opts runOptions, svc *daemonService) func() {
	if opts.ipcSocket == "" {
		logger.Printf("IPC control plane disabled (no --ipc-socket)")
		return func() {}
	}

	tokenPath := opts.ipcTokenFile
	if tokenPath == "" {
		// Default: a `control-token` file next to the socket. Production
		// installs colocate both inside /var/run/hsh/ so a single chown
		// of the runtime dir grants the `hsh` group read access to
		// both. Dev runs that pass --ipc-socket=/tmp/foo/hsh.sock get
		// /tmp/foo/control-token, which keeps everything under the
		// caller's tmpdir.
		tokenPath = filepath.Join(filepath.Dir(opts.ipcSocket), "control-token")
	}

	store, err := ipc.NewFileTokenStore(tokenPath, ipc.FileTokenOptions{})
	if err != nil {
		logger.Printf("IPC disabled: %v", err)
		return func() {}
	}
	if _, err := store.Rotate(); err != nil {
		logger.Printf("IPC disabled: rotate token: %v", err)
		return func() {}
	}

	srv, err := ipc.NewServer(ipc.ServerOptions{
		Service:    svc,
		TokenStore: store,
		Logger:     logger,
	})
	if err != nil {
		logger.Printf("IPC disabled: %v", err)
		return func() {}
	}

	ln, err := ipc.Listen(ipc.ListenerOptions{
		Path:      opts.ipcSocket,
		GroupName: opts.ipcGroup,
	})
	if err != nil {
		logger.Printf("IPC disabled: %v", err)
		return func() {}
	}
	logger.Printf("IPC control plane listening at %s (token %s)", opts.ipcSocket, tokenPath)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	// Return a closer that performs an orderly shutdown. We give the
	// server 5 seconds to drain in-flight requests; longer than that
	// likely means a wedged client, which we'd rather drop than block
	// the daemon's exit.
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Printf("IPC shutdown: %v", err)
		}
		// Best-effort remove of the socket file so a restart doesn't
		// hit the stale-socket cleanup path.
		_ = os.Remove(opts.ipcSocket)
		// Drain the serve error so the goroutine can exit.
		select {
		case <-serveErr:
		case <-time.After(time.Second):
		}
	}
}


