// Package portmap defines which TCP ports the tunnel accepts for each
// connection subtype. The rule is simple: each protocol has a canonical
// port (5432 for postgres, 3306 for mysql, etc.) and the tunnel refuses
// to forward flows that target any other port.
//
// Why enforce this in the tunnel
//
// Without a port check, a user typing `psql -h mysql-prod.hoop` would
// have psql connect to the MySQL host's IP on port 5432. gVisor would
// accept that flow and the tunnel would happily open a gRPC pipe to the
// MySQL agent — which then fails inside the agent when it tries to
// speak postgres to a MySQL upstream. The resulting error ("connection
// refused" with the wrong port) is confusing because it appears to come
// from the agent rather than from the client's protocol mistake.
//
// Rejecting at the netstack TCP-accept layer gives the client a fast
// local ECONNREFUSED with the obvious message: wrong port for this
// host. It also avoids burning a gateway round-trip and an agent
// upstream dial on a request that cannot succeed.
//
// The `tcp` subtype is intentionally exempt: a generic TCP connection
// can target any port the connection author configured at the agent
// end. There is no canonical port for "tcp" — so we accept any.
package portmap

import (
	pb "github.com/hoophq/hoop/common/proto"
)

// CanonicalPort returns the well-known TCP port for the given hoop
// connection subtype. Returns (0, false) for subtypes that have no
// canonical port (e.g. "tcp") or are not tunnelable at all.
//
// Callers should treat (0, false) as "accept any port" for tunnelable
// subtypes and "reject" for non-tunnelable subtypes; differentiate
// using the tunnelable allowlist (see tunnel/client.isTunnelableSubType).
func CanonicalPort(subType string) (uint16, bool) {
	switch pb.ConnectionType(subType) {
	case pb.ConnectionTypePostgres:
		return 5432, true
	case pb.ConnectionTypeMySQL:
		return 3306, true
	case pb.ConnectionTypeMSSQL:
		return 1433, true
	case pb.ConnectionTypeMongoDB:
		return 27017, true
	case pb.ConnectionTypeOracleDB:
		return 1521, true
	case pb.ConnectionTypeHttpProxy:
		// Clients speak plain HTTP to the tunnel (the agent terminates
		// TLS to the real upstream), so port 80 is the only valid
		// target. 443 is deliberately rejected: the tunnel has no
		// certificate for *.hoop, so an https:// URL can never work
		// and should fail fast at the SYN.
		return 80, true
	}
	return 0, false
}

// IsAcceptedPort returns true if the tunnel should forward a TCP flow
// destined for the given (subType, port). The decision is:
//
//   - subType has a canonical port: accept only if port matches.
//   - subType is `tcp`: accept any non-zero port (user-defined upstream).
//   - subType is anything else (not tunnelable): reject.
//
// This is the single source of truth the tunnel uses when filtering
// gVisor-accepted TCP connections.
func IsAcceptedPort(subType string, port uint16) bool {
	if port == 0 {
		return false
	}
	if canonical, ok := CanonicalPort(subType); ok {
		return port == canonical
	}
	// Generic TCP connections accept any port; everything else is rejected.
	return pb.ConnectionType(subType) == pb.ConnectionTypeTCP
}
