package tunnelmgr

import (
	"context"
	"net/netip"

	"github.com/hoophq/hoop/common/grpc"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/client"
	"github.com/hoophq/hoop/tunnel/netstack"
	"github.com/hoophq/hoop/tunnel/portmap"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

// makeAcceptFunc returns the netstack accept-policy callback. It is
// consulted at SYN time, *before* the 3-way handshake completes:
// returning false makes gVisor send a RST so the client sees a clean
// ECONNREFUSED at the TCP layer.
//
// The policy is: drop SYNs to unallocated addresses, and drop SYNs that
// target a port that doesn't match the connection subtype's canonical
// port (e.g. `psql -h mysql-prod.hoop` lands on TCP/5432 against a
// MySQL connection; we reject before opening the upstream).
func (m *Manager) makeAcceptFunc(
	alloc *addressing.Allocator,
	registry *connRegistry,
) netstack.AcceptFunc {
	logger := m.opts.Logger
	return func(localAddr netip.Addr, localPort uint16) bool {
		name, ok := alloc.LookupAddr(localAddr)
		if !ok {
			logger.Printf("tunnelmgr: reject SYN %s:%d — unmapped address", localAddr, localPort)
			return false
		}
		// subTypeOf returns ok=false for unknown names AND for
		// connections that were deleted on the gateway (marked inactive
		// by a refresh). Either way we refuse to open a pipe to a
		// connection the gateway no longer offers.
		subType, ok := registry.subTypeOf(name)
		if !ok {
			logger.Printf("tunnelmgr: reject SYN %s:%d -> %s — connection unknown or no longer active", localAddr, localPort, name)
			return false
		}
		if !portmap.IsAcceptedPort(subType, localPort) {
			if expected, hasCanonical := portmap.CanonicalPort(subType); hasCanonical {
				logger.Printf("tunnelmgr: reject SYN %s:%d -> %s (%s) — wrong port, expected %d",
					localAddr, localPort, name, subType, expected)
			} else {
				logger.Printf("tunnelmgr: reject SYN %s:%d -> %s (%s) — port not allowed",
					localAddr, localPort, name, subType)
			}
			return false
		}
		return true
	}
}

// makeTCPHandler returns the netstack TCP forwarder callback. By the
// time this runs, makeAcceptFunc has already validated the (addr,
// port) pair, so all we need to do is resolve the name and open the
// per-flow gRPC pipe.
func (m *Manager) makeTCPHandler(
	alloc *addressing.Allocator,
	registry *connRegistry,
	gatewayCfg grpc.ClientConfig,
) netstack.Handler {
	logger := m.opts.Logger
	userAgent := m.opts.UserAgent
	return func(conn *gonet.TCPConn, localAddr netip.Addr, localPort uint16) {
		defer conn.Close()
		name, ok := alloc.LookupAddr(localAddr)
		if !ok {
			// Defensive: makeAcceptFunc should have rejected this SYN.
			logger.Printf("tunnelmgr: inbound TCP to unmapped address %s — dropping", localAddr)
			return
		}
		subType, _ := registry.subTypeOf(name)
		logger.Printf("tunnelmgr: accept %s:%d -> %s (%s)", localAddr, localPort, name, subType)
		err := client.DialAndPipe(context.Background(), conn, client.PipeOptions{
			GatewayConfig:  gatewayCfg,
			ConnectionName: name,
			UserAgent:      userAgent,
		})
		if err != nil {
			logger.Printf("tunnelmgr: pipe %s closed: %v", name, err)
			return
		}
		logger.Printf("tunnelmgr: pipe %s closed cleanly", name)
	}
}
