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
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/client"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/netstack"
	"github.com/hoophq/hoop/tunnel/portmap"
	"github.com/hoophq/hoop/tunnel/resolver"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
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
	tld := flag.String("tld", resolver.DefaultTLD, "TLD owned by the tunnel (HSH_TUNNEL_DOMAIN overrides)")
	devName := flag.String("dev", "", "TUN device name (kernel picks if empty)")
	sessionSeed := flag.String("session", "spike-session", "session seed (controls the /48 prefix)")
	// ipcSocket and ipcTokenFile control the local control plane (RD-215).
	// Empty `--ipc-socket` skips the IPC layer entirely, which keeps the
	// daemon usable for standalone dev / integration tests where the
	// operator does not want to touch /var/run.
	ipcSocket := flag.String("ipc-socket", "", "path of the IPC unix socket (empty disables IPC; default in production: "+ipc.DefaultSocketPathUnix+")")
	ipcTokenFile := flag.String("ipc-token-file", "", "path of the control-token file (defaults to <dir-of-ipc-socket>/hsh/control-token)")
	ipcGroup := flag.String("ipc-group", "", "OS group that owns the IPC socket (members can connect; empty leaves it owned by the daemon's primary group)")
	flag.Parse()

	if env := os.Getenv("HSH_TUNNEL_DOMAIN"); env != "" {
		*tld = env
	}

	logger := log.New(os.Stderr, "hsh-tunneld ", log.LstdFlags|log.Lmicroseconds)

	cfg := runOptions{
		tld:          *tld,
		devName:      *devName,
		sessionSeed:  *sessionSeed,
		ipcSocket:    *ipcSocket,
		ipcTokenFile: *ipcTokenFile,
		ipcGroup:     *ipcGroup,
	}
	if err := run(logger, cfg); err != nil {
		logger.Fatal(err)
	}
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
}

func run(logger *log.Logger, opts runOptions) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	gatewayCfg, apiBase, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	logger.Printf("gateway gRPC %s (insecure=%v, tls_skip_verify=%v) api %s",
		gatewayCfg.ServerAddress, gatewayCfg.Insecure, gatewayCfg.TLSSkipVerify, apiBase)

	alloc := addressing.New(opts.sessionSeed)
	logger.Printf("session prefix %s gateway %s", alloc.Prefix(), alloc.Gateway())

	// Fetch the connection list from the gateway's existing /api/connections
	// endpoint. We allocate IPs for every tunnelable name up-front so the
	// resolver can answer without any per-query gateway round-trip.
	conns, err := client.FetchConnections(ctx, client.FetchConnectionsOptions{
		APIBaseURL: apiBase,
		Token:      gatewayCfg.Token,
	})
	if err != nil {
		return fmt.Errorf("fetch connections: %w", err)
	}
	if len(conns) == 0 {
		return errors.New("no tunnelable connections found for this user")
	}
	// subTypeByName lets the TCP handler look up the connection subtype
	// (postgres, mysql, ...) from the name resolved out of the destination
	// IP. We keep this map alongside the allocator rather than embedding
	// the subtype inside it: the allocator's responsibility is name <-> IP,
	// and conflating concerns would bleed protocol semantics into a pure
	// addressing component.
	subTypeByName := make(map[string]string, len(conns))
	for _, c := range conns {
		if _, err := alloc.AddName(c.Name); err != nil {
			return fmt.Errorf("allocate %s: %w", c.Name, err)
		}
		subTypeByName[c.Name] = c.SubType
	}
	logger.Printf("loaded %d tunnelable connection(s):", len(conns))
	for _, c := range conns {
		ip, _ := alloc.LookupName(c.Name)
		port, hasCanonical := portmap.CanonicalPort(c.SubType)
		portDesc := "any port"
		if hasCanonical {
			portDesc = fmt.Sprintf("port %d", port)
		}
		logger.Printf("  %s.%s (%s, %s) -> %s", c.Name, opts.tld, c.SubType, portDesc, ip)
	}

	// Build the daemon-side IPC service implementation BEFORE starting
	// the netstack so the control plane can serve /v1/status (with
	// Running=false) during initialisation. We flip the running flag
	// after the netstack is fully wired.
	svc := newDaemonService(alloc, subTypeByName, apiBase, os.Getenv("HOOP_GRPCURL"), gatewayCfg.Token != "")

	// Start the IPC control plane in a goroutine if configured. Failure
	// to start the IPC is logged but does not stop the daemon: the
	// tunnel itself remains the primary product, and a missing control
	// surface is recoverable by the operator. shutdownIPC is a no-op if
	// IPC was disabled.
	shutdownIPC := startIPCServer(ctx, logger, opts, svc)
	defer shutdownIPC()

	// Bring up the netstack + TUN device.
	rsvr := resolver.New(alloc, opts.tld)
	ns, err := netstack.New(ctx, netstack.Options{
		Prefix:     alloc.Prefix(),
		Gateway:    alloc.Gateway(),
		DeviceName: opts.devName,
		TCPAccept:  makeAcceptFunc(logger, alloc, subTypeByName),
		TCPHandler: makeTCPHandler(logger, alloc, subTypeByName, gatewayCfg),
		DNSHandler: rsvr.HandleUDP,
	})
	if err != nil {
		return fmt.Errorf("netstack: %w", err)
	}
	defer ns.Close()

	actualDev := ns.DeviceName()
	if err := netstack.ConfigureRoutes(actualDev, alloc.Prefix().String(), alloc.HostAddr().String()); err != nil {
		return fmt.Errorf("configure routes: %w", err)
	}
	defer netstack.UnconfigureRoutes(actualDev, alloc.Prefix().String(), alloc.HostAddr().String())

	// Netstack is up and accepting flows — surface that to the UI.
	svc.MarkRunning()

	logger.Printf("tunnel is up.")
	logger.Printf("  host addr: %s (tun0)", alloc.HostAddr())
	logger.Printf("  resolver:  %s:53 (gVisor)", alloc.Gateway())
	logger.Printf("  try:       dig @%s %s.%s AAAA", alloc.Gateway(), conns[0].Name, opts.tld)
	logger.Printf("")
	logger.Printf("To route *.%s through this resolver host-wide (systemd-resolved):", opts.tld)
	logger.Printf("  sudo resolvectl dns %s %s", actualDev, alloc.Gateway())
	logger.Printf("  sudo resolvectl domain %s '~%s'", actualDev, opts.tld)
	logger.Printf("After that:  psql -h %s.%s ...   (or any *.%s host)", conns[0].Name, opts.tld, opts.tld)

	<-ctx.Done()
	logger.Printf("shutting down")
	return nil
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
		// Default: /var/run/hsh/control-token (next to the socket).
		tokenPath = filepath.Join(filepath.Dir(opts.ipcSocket), "hsh", "control-token")
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

// makeAcceptFunc returns the netstack accept-policy callback. It is
// consulted at SYN time, *before* the 3-way handshake completes:
// returning false makes gVisor send a RST so the client sees a clean
// ECONNREFUSED at the TCP layer.
//
// The policy is: drop SYNs to unallocated addresses, and drop SYNs that
// target a port that doesn't match the connection subtype's canonical
// port (e.g. `psql -h mysql-prod.hoop` lands on TCP/5432 against a MySQL
// connection; we reject before opening the upstream).
func makeAcceptFunc(
	logger *log.Logger,
	alloc *addressing.Allocator,
	subTypeByName map[string]string,
) netstack.AcceptFunc {
	return func(localAddr netip.Addr, localPort uint16) bool {
		name, ok := alloc.LookupAddr(localAddr)
		if !ok {
			logger.Printf("reject SYN %s:%d: unmapped address", localAddr, localPort)
			return false
		}
		subType, ok := subTypeByName[name]
		if !ok {
			// Should never happen: every allocated name was inserted
			// into subTypeByName at startup. Fail closed.
			logger.Printf("reject SYN %s:%d -> %s: no subtype recorded", localAddr, localPort, name)
			return false
		}
		if !portmap.IsAcceptedPort(subType, localPort) {
			if expected, hasCanonical := portmap.CanonicalPort(subType); hasCanonical {
				logger.Printf("reject SYN %s:%d -> %s (%s): wrong port, expected %d",
					localAddr, localPort, name, subType, expected)
			} else {
				logger.Printf("reject SYN %s:%d -> %s (%s): port not allowed",
					localAddr, localPort, name, subType)
			}
			return false
		}
		return true
	}
}

// makeTCPHandler returns the netstack TCP forwarder callback. By the
// time this runs, makeAcceptFunc has already validated the (addr, port)
// pair, so all we need to do is resolve the name and open the per-flow
// gRPC pipe.
func makeTCPHandler(
	logger *log.Logger,
	alloc *addressing.Allocator,
	subTypeByName map[string]string,
	gatewayCfg grpc.ClientConfig,
) netstack.Handler {
	return func(conn *gonet.TCPConn, localAddr netip.Addr, localPort uint16) {
		defer conn.Close()
		name, ok := alloc.LookupAddr(localAddr)
		if !ok {
			// Defensive: makeAcceptFunc should have rejected this SYN.
			logger.Printf("inbound TCP to unmapped address %s — dropping", localAddr)
			return
		}
		subType := subTypeByName[name]
		logger.Printf("accept %s:%d -> %s (%s)", localAddr, localPort, name, subType)
		err := client.DialAndPipe(context.Background(), conn, client.PipeOptions{
			GatewayConfig:  gatewayCfg,
			ConnectionName: name,
			UserAgent:      userAgent(),
		})
		if err != nil {
			logger.Printf("pipe %s closed: %v", name, err)
			return
		}
		logger.Printf("pipe %s closed cleanly", name)
	}
}

// loadConfig builds a gRPC client config using the same environment variables
// the `hoop` CLI honors. HOOP_APIURL and HOOP_TOKEN are required. HOOP_GRPCURL
// is optional: when absent the gRPC address is fetched from GET /api/serverinfo
// automatically, which is the same mechanism the hoop CLI uses after login.
func loadConfig(ctx context.Context) (grpc.ClientConfig, string, error) {
	apiURL := os.Getenv("HOOP_APIURL")
	token := os.Getenv("HOOP_TOKEN")
	if apiURL == "" || token == "" {
		return grpc.ClientConfig{}, "", errors.New("HOOP_APIURL and HOOP_TOKEN are required")
	}
	apiBase := strings.TrimRight(apiURL, "/")

	grpcURL := os.Getenv("HOOP_GRPCURL")
	if grpcURL == "" {
		si, err := client.FetchServerInfo(ctx, client.FetchServerInfoOptions{
			APIBaseURL: apiBase,
			Token:      token,
		})
		if err != nil {
			return grpc.ClientConfig{}, "", fmt.Errorf("fetch serverinfo: %w", err)
		}
		grpcURL = si.GrpcURL
	}

	srvAddr, err := grpc.ParseServerAddress(grpcURL)
	if err != nil {
		return grpc.ClientConfig{}, "", fmt.Errorf("parse gRPC URL %q: %w", grpcURL, err)
	}
	cfg := grpc.ClientConfig{
		ServerAddress: srvAddr,
		Token:         token,
		Insecure:      isInsecureScheme(grpcURL),
		TLSSkipVerify: os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true",
		TLSServerName: os.Getenv("HOOP_TLSSERVERNAME"),
	}
	return cfg, apiBase, nil
}

// isInsecureScheme returns true for schemes that use plain-text gRPC:
// "http://" or "grpc://". Everything else (bare HOST:PORT, "https://",
// "grpcs://") implies TLS, matching the hoop CLI's hasInsecureScheme.
func isInsecureScheme(grpcURL string) bool {
	low := strings.ToLower(grpcURL)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "grpc://")
}
