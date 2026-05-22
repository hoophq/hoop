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
	"strings"
	"syscall"

	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/client"
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
	flag.Parse()

	if env := os.Getenv("HSH_TUNNEL_DOMAIN"); env != "" {
		*tld = env
	}

	logger := log.New(os.Stderr, "hsh-tunneld ", log.LstdFlags|log.Lmicroseconds)

	if err := run(logger, *tld, *devName, *sessionSeed); err != nil {
		logger.Fatal(err)
	}
}

func run(logger *log.Logger, tld, devName, sessionSeed string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	gatewayCfg, apiBase, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	logger.Printf("gateway gRPC %s (insecure=%v, tls_skip_verify=%v) api %s",
		gatewayCfg.ServerAddress, gatewayCfg.Insecure, gatewayCfg.TLSSkipVerify, apiBase)

	alloc := addressing.New(sessionSeed)
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
		logger.Printf("  %s.%s (%s, %s) -> %s", c.Name, tld, c.SubType, portDesc, ip)
	}

	// Bring up the netstack + TUN device.
	rsvr := resolver.New(alloc, tld)
	ns, err := netstack.New(ctx, netstack.Options{
		Prefix:     alloc.Prefix(),
		Gateway:    alloc.Gateway(),
		DeviceName: devName,
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

	logger.Printf("tunnel is up.")
	logger.Printf("  host addr: %s (tun0)", alloc.HostAddr())
	logger.Printf("  resolver:  %s:53 (gVisor)", alloc.Gateway())
	logger.Printf("  try:       dig @%s %s.%s AAAA", alloc.Gateway(), conns[0].Name, tld)
	logger.Printf("")
	logger.Printf("To route *.%s through this resolver host-wide (systemd-resolved):", tld)
	logger.Printf("  sudo resolvectl dns %s %s", actualDev, alloc.Gateway())
	logger.Printf("  sudo resolvectl domain %s '~%s'", actualDev, tld)
	logger.Printf("After that:  psql -h %s.%s ...   (or any *.%s host)", conns[0].Name, tld, tld)

	<-ctx.Done()
	logger.Printf("shutting down")
	return nil
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
