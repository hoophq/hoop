// Command hsh-tunnel is the RD-176 spike binary. It opens a tunnel session
// against a stub gateway, configures a userspace netstack + TUN device, and
// routes every TCP connection to *.hoop addresses through the tunnel.
//
// This binary will be folded into the `hsh` CLI (RD-183) once the spike
// validates. For now it is a standalone main.
//
// Linux usage:
//
//	# Build
//	go build ./tunnel/cmd/hsh-tunnel
//
//	# Run (requires CAP_NET_ADMIN; sudo is easiest for the spike)
//	sudo ./hsh-tunnel -gateway ws://127.0.0.1:7575/api/tunnel
//
//	# Or grant the capability once and run unprivileged:
//	sudo setcap cap_net_admin+ep ./hsh-tunnel
//	./hsh-tunnel -gateway ws://127.0.0.1:7575/api/tunnel
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/client"
	"github.com/hoophq/hoop/tunnel/netstack"
	"github.com/hoophq/hoop/tunnel/resolver"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

func main() {
	gateway := flag.String("gateway", "ws://127.0.0.1:7575/api/tunnel", "gateway WebSocket URL")
	tld := flag.String("tld", resolver.DefaultTLD, "TLD owned by the tunnel (HSH_TUNNEL_DOMAIN overrides)")
	devName := flag.String("dev", "", "TUN device name (kernel picks if empty)")
	session := flag.String("session", "spike-session", "session seed (controls the /48 prefix)")
	flag.Parse()

	if env := os.Getenv("HSH_TUNNEL_DOMAIN"); env != "" {
		*tld = env
	}

	logger := log.New(os.Stderr, "hsh-tunnel ", log.LstdFlags|log.Lmicroseconds)

	if err := run(logger, *gateway, *tld, *devName, *session); err != nil {
		logger.Fatal(err)
	}
}

func run(logger *log.Logger, gatewayURL, tld, devName, sessionSeed string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	alloc := addressing.New(sessionSeed)
	logger.Printf("session prefix %s gateway %s", alloc.Prefix(), alloc.Gateway())

	// Fetch the connection list from the stub gateway's /connections
	// endpoint. We allocate IPs for every name up-front so the resolver
	// answers without a round-trip.
	if err := loadConnections(ctx, alloc, gatewayURL); err != nil {
		return fmt.Errorf("load connections: %w", err)
	}
	logger.Printf("loaded %d connection(s):", len(alloc.Names()))
	for _, n := range alloc.Names() {
		ip, _ := alloc.LookupName(n)
		logger.Printf("  %s.%s -> %s", n, tld, ip)
	}

	// Open the tunnel session.
	sess, err := client.Dial(ctx, gatewayURL, client.DialOptions{HandshakeTimeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
	}
	defer sess.Close()
	logger.Printf("tunnel session opened")

	// Bring up the netstack + TUN device.
	rsvr := resolver.New(alloc, tld)
	ns, err := netstack.New(ctx, netstack.Options{
		Prefix:     alloc.Prefix(),
		Gateway:    alloc.Gateway(),
		DeviceName: devName,
		TCPHandler: makeTCPHandler(logger, alloc, sess),
		DNSHandler: rsvr.HandleUDP,
	})
	if err != nil {
		return fmt.Errorf("netstack: %w", err)
	}
	defer ns.Close()

	actualDev := ns.DeviceName()
	// Configure host routing so the kernel sends *.hoop traffic into the
	// TUN device.
	if err := netstack.ConfigureRoutes(actualDev, alloc.Prefix().String(), alloc.Gateway().String()); err != nil {
		return fmt.Errorf("configure routes: %w", err)
	}
	defer netstack.UnconfigureRoutes(actualDev, alloc.Prefix().String(), alloc.Gateway().String())

	logger.Printf("tunnel is up.")
	logger.Printf("  resolver:  %s:53", alloc.Gateway())
	logger.Printf("  try:       dig @%s pg-prod.%s AAAA", alloc.Gateway(), tld)
	logger.Printf("")
	logger.Printf("To route *.%s through this resolver host-wide (systemd-resolved):", tld)
	logger.Printf("  sudo resolvectl dns %s %s", actualDev, alloc.Gateway())
	logger.Printf("  sudo resolvectl domain %s '~%s'", actualDev, tld)
	logger.Printf("After that:  psql -h pg-prod.%s ...   (or any *.%s host)", tld, tld)

	<-ctx.Done()
	logger.Printf("shutting down")
	return nil
}

// makeTCPHandler returns the netstack TCP forwarder callback. For each
// accepted connection it looks up the destination IP, opens a tunnel stream
// to the resolved name, and bridges bytes both ways.
func makeTCPHandler(logger *log.Logger, alloc *addressing.Allocator, sess *client.Session) netstack.Handler {
	return func(conn *gonet.TCPConn, localAddr netip.Addr, localPort uint16) {
		defer conn.Close()
		name, ok := alloc.LookupAddr(localAddr)
		if !ok {
			logger.Printf("inbound TCP to unmapped address %s — dropping", localAddr)
			return
		}
		stream, err := sess.OpenStream(name)
		if err != nil {
			logger.Printf("open stream for %s: %v", name, err)
			return
		}
		defer stream.Close()

		errCh := make(chan error, 2)
		go func() {
			_, err := io.Copy(stream, conn)
			_ = stream.CloseWrite()
			errCh <- err
		}()
		go func() {
			_, err := io.Copy(conn, stream)
			errCh <- err
		}()
		<-errCh
	}
}

// loadConnections fetches the connection list from the gateway's stub
// /connections endpoint (HTTP, same origin as the WS URL). RD-182 will
// replace this with the real authenticated endpoint.
func loadConnections(ctx context.Context, alloc *addressing.Allocator, wsURL string) error {
	u, err := url.Parse(wsURL)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	u.Path = strings.TrimSuffix(u.Path, "/api/tunnel") + "/api/tunnel/connections"

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connections endpoint returned %s", resp.Status)
	}
	var list []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	for _, item := range list {
		if _, err := alloc.AddName(item.Name); err != nil {
			return err
		}
	}
	return nil
}
