//go:build integration

package transport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	commongrpc "github.com/hoophq/hoop/common/grpc"
	tunnelclient "github.com/hoophq/hoop/tunnel/client"
)

// The fixed placeholders the tunnel advertises. Duplicated here rather than
// imported: they are the tunnel's own contract (tunnel/tunnelmgr), and this
// suite asserts the value a real client must present, so a shared constant
// would let both sides drift together without the test noticing.
const (
	tunnelUser     = "noop"
	tunnelPassword = "noop"
)

// TestTunnelAcceptsFixedCredentials is the DEP-142 acceptance test.
//
// It is the whole stack, unmocked: a real `lib/pq` client speaks the
// PostgreSQL wire protocol to a local listener, every accepted flow is handed
// to the tunnel's own client.DialAndPipe (the exact call the daemon's netstack
// handler makes), which opens a gRPC session to a real gateway, reaches a real
// agent controller, and lands on a real PostgreSQL container.
//
// The client authenticates with the fixed noop/noop placeholders that
// `hsh tunnel ls` advertises, and never sees the database's real
// credentials. If the tunnel routed this flow onto the raw TCP relay (the
// pre-DEP-142 behaviour) the client would be handed the backend's own SASL
// challenge and the query would fail.
func TestTunnelAcceptsFixedCredentials(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("tune2e")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)
			startAgent(t, c, dsn)
			waitConnectionOnline(t, connName)

			addr := startTunnelListener(t, connName)

			// Exactly what a user would run against <name>.hoop, except the
			// host is the local listener standing in for the netstack's
			// virtual IP (the netstack itself needs root and a TUN device).
			dsnStr := fmt.Sprintf(
				"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
				addr.host, addr.port, tunnelUser, tunnelPassword, gw.Postgres.Database,
			)
			db, err := sql.Open("postgres", dsnStr)
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var got int
			if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&got); err != nil {
				skipWithoutEnterpriseLibhoop(t, err)
				t.Fatalf("SELECT 1 through the tunnel with %s/%s: %v",
					tunnelUser, tunnelPassword, err)
			}
			if got != 1 {
				t.Fatalf("SELECT 1 returned %d", got)
			}

			// A second query on the same pooled connection proves the pipe
			// stayed framed after the handshake, not just through it.
			var two int
			if err := db.QueryRowContext(ctx, "SELECT 2").Scan(&two); err != nil {
				t.Fatalf("second query through the tunnel: %v", err)
			}
			if two != 2 {
				t.Fatalf("SELECT 2 returned %d", two)
			}
		})
	}
}

// TestTunnelEnforcesGuardrails pins the security consequence of routing
// database flows over their protocol packet family.
//
// Guardrails are evaluated by the agent's protocol proxy, which has to parse
// the query to apply them. The raw TCP relay cannot: it copies bytes verbatim,
// which is why agent/controller/tcp.go refuses guarded sessions outright
// (DEP-48). Before this change every tunnelled database flow took that relay,
// so a guarded connection was simply unusable over the tunnel.
//
// Now the flow reaches the real proxy, so the rule is enforced: a denied query
// is blocked while the session stays open for the client to try another.
func TestTunnelEnforcesGuardrails(t *testing.T) {
	c := transports()[0] // agent-side enforcement; wire-agnostic, gRPC suffices

	connName := uniqueName("tunguard")
	agentID, dsn := createAgent(t, uniqueName("agent"))
	createPGConnection(t, connName, agentID)
	// Deny any statement containing "SELECT".
	createGuardrailForConnection(t, uniqueName("gr"), connectionID(t, connName))
	startAgent(t, c, dsn)
	waitConnectionOnline(t, connName)

	addr := startTunnelListener(t, connName)

	dsnStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		addr.host, addr.port, tunnelUser, tunnelPassword, gw.Postgres.Database,
	)
	db, err := sql.Open("postgres", dsnStr)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var got int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&got)
	if err == nil {
		t.Fatal("a guardrail-denied query succeeded through the tunnel; the flow is " +
			"bypassing the agent's protocol proxy")
	}
	// Assert on the rule's own message, not merely on "some error": without
	// the enterprise libhoop the proxy cannot be built at all and the session
	// dies with a bare EOF, which would satisfy a weaker check while proving
	// nothing about enforcement.
	if !strings.Contains(err.Error(), "Blocked by the following Guardrails rule") {
		skipWithoutEnterpriseLibhoop(t, err)
		t.Fatalf("query failed, but not with a guardrail block: %v", err)
	}
	t.Logf("guardrail enforced over the tunnel: %v", err)
}

// TestTunnelIgnoresClientCredentials proves the successful case above is not
// passing by accident — i.e. that credential *injection* really happened.
//
// The agent's protocol proxy terminates the client's authentication locally
// and never forwards what the client typed; it authenticates upstream with the
// secrets stored on the connection. Two observable consequences follow, and
// this test pins both:
//
//  1. A deliberately wrong password still connects, because the client's
//     credentials never reach the database.
//  2. The resulting session runs as the connection's stored database user,
//     not as whatever the client claimed to be.
//
// (2) is the load-bearing assertion: if the tunnel were passing bytes through
// to a permissive backend, current_user would reflect the client's name.
func TestTunnelIgnoresClientCredentials(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("tunneg")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)
			startAgent(t, c, dsn)
			waitConnectionOnline(t, connName)

			addr := startTunnelListener(t, connName)

			// Neither the username nor the password is valid upstream.
			const bogusUser = "definitely-not-a-database-role"
			dsnStr := fmt.Sprintf(
				"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
				addr.host, addr.port, bogusUser, "wrong-password", gw.Postgres.Database,
			)
			db, err := sql.Open("postgres", dsnStr)
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var upstreamUser string
			if err := db.QueryRowContext(ctx, "SELECT current_user").Scan(&upstreamUser); err != nil {
				skipWithoutEnterpriseLibhoop(t, err)
				t.Fatalf("query through the tunnel with bogus client credentials: %v", err)
			}
			if upstreamUser == bogusUser {
				t.Fatalf("the session runs as the client-supplied role %q; the tunnel forwarded "+
					"client credentials instead of injecting the connection's own", bogusUser)
			}
			if upstreamUser != gw.Postgres.User {
				t.Fatalf("current_user = %q, want the connection's stored user %q",
					upstreamUser, gw.Postgres.User)
			}
		})
	}
}

// skipWithoutEnterpriseLibhoop skips the calling test when a tunnel query
// failed only because the agent could not build a protocol proxy.
//
// The proxies live in the enterprise libhoop (checked out by the integration
// CI job); the OSS stub returns "missing protocol hoop library" for every
// protocol, and the session then dies with a bare EOF at the client. Every
// tunnel test here is about what the proxy does, so there is nothing to assert
// without it — the same OSS/enterprise split the PG round-trip suite uses.
//
// It deliberately does NOT swallow other failures: only EOF-shaped and
// stub-marked errors skip, so a genuine regression still fails.
func skipWithoutEnterpriseLibhoop(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), ossLibhoopMarker) || errors.Is(err, io.EOF) {
		t.Skip("tunnel protocol-proxy tests require the enterprise libhoop; skipping on the OSS stub")
	}
}

type listenerAddr struct{ host, port string }

// startTunnelListener accepts TCP flows on loopback and pipes each one through
// the tunnel's client.DialAndPipe, which is what the daemon's netstack handler
// does per accepted flow (tunnel/tunnelmgr/handlers.go).
//
// It stands in for the gVisor netstack only: the netstack needs root and a TUN
// device, and it contributes nothing to what these tests assert. Everything
// downstream of the accept — the pipe, the packet-family routing, the framing,
// the gateway, the agent and the database — is the production path.
func startTunnelListener(t *testing.T, connName string) listenerAddr {
	t.Helper()

	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Pipe goroutines outlive the last query: closing the listener and
	// cancelling only starts their teardown. Wait for them before the test
	// returns, otherwise a late t.Logf panics with "Log in goroutine after
	// test has completed".
	var pipes sync.WaitGroup
	accepted := make(chan struct{})
	var acceptOnce sync.Once

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return // listener closed by cleanup
			}
			acceptOnce.Do(func() { close(accepted) })
			pipes.Add(1)
			go func() {
				defer pipes.Done()
				defer conn.Close()
				err := tunnelclient.DialAndPipe(ctx, conn, tunnelclient.PipeOptions{
					GatewayConfig: commongrpc.ClientConfig{
						ServerAddress: gw.GRPCAddr,
						Token:         adminToken(t),
						UserAgent:     "hoop-tunnel-itest",
						Insecure:      true,
					},
					ConnectionName:     connName,
					SessionOpenTimeout: 20 * time.Second,
				})
				// Deliberately not asserted or logged: a pipe always ends in
				// an error once the harness tears the agent down, and logging
				// here would race the test's completion. The queries above
				// are what decide the verdict.
				_ = err
			}()
		}
	}()

	t.Cleanup(func() {
		_ = lis.Close()
		cancel()
		pipes.Wait()
		// Surface pipe failures only when a flow never got off the ground;
		// a test that queried successfully does not care how its pipe ended.
		select {
		case <-accepted:
		default:
			t.Errorf("no flow ever reached the tunnel pipe")
		}
	})

	host, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", lis.Addr(), err)
	}
	return listenerAddr{host: host, port: port}
}
