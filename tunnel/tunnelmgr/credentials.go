package tunnelmgr

import pb "github.com/hoophq/hoop/common/proto"

// The fixed credentials a native client presents to reach a connection
// through the tunnel.
//
// These are not the connection's real credentials and not the gateway's
// rotating token. The agent's protocol proxy terminates the client's
// authentication locally and re-authenticates upstream with the secrets
// stored on the connection, so whatever the client presents at the local end
// is a placeholder. Standardising it lets the CLI hand the user a
// ready-to-paste connection string. `hoop connect` prints the same pair for
// its loopback listener.
//
// They authorise nothing on their own: what authorises the session is having
// logged the daemon in. Every flow still crosses the gateway as a normal
// session with the usual access control, audit, guardrail, and DLP handling.
//
// NOT the gateway's native-access proxies: those take a per-user, expiring
// secret key that the gateway resolves against the connection credentials
// table, and they reject these values. Keep the two straight.
const (
	credentialUser     = "noop"
	credentialPassword = "noop"
)

// credentialsFor returns the fixed credentials for a connection subtype.
//
// Empty strings mean the client authenticates outside the protocol:
//
//   - tcp: an opaque user-defined upstream. Hoop cannot parse the protocol,
//     so the flow is relayed verbatim and any credentials are the client's own.
//   - httpproxy: authorization rides HTTP headers the agent injects.
func credentialsFor(subType string) (username, password string) {
	switch pb.ConnectionType(subType) {
	case pb.ConnectionTypePostgres,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB:
		return credentialUser, credentialPassword
	}
	return "", ""
}
