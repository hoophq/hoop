package tunnelmgr

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// Every subtype whose bytes flow through an agent-side protocol proxy accepts
// the placeholder locally, because that proxy re-authenticates upstream with
// the connection's stored secrets.
func TestCredentialsForProxiedProtocols(t *testing.T) {
	for _, subType := range []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB,
	} {
		user, pass := credentialsFor(subType.String())
		if user != credentialUser || pass != credentialPassword {
			t.Errorf("%s: got %q/%q, want %q/%q",
				subType, user, pass, credentialUser, credentialPassword)
		}
	}
}

// Subtypes with no protocol proxy must report nothing rather than advertising
// a placeholder that would not work.
//
// `tcp` is the one that matters: it is tunnelable but relayed verbatim, so the
// client faces the upstream's own authentication and needs real credentials.
func TestCredentialsForUnproxiedProtocols(t *testing.T) {
	for _, subType := range []pb.ConnectionType{
		pb.ConnectionTypeTCP,
		pb.ConnectionTypeHttpProxy,
		pb.ConnectionTypeSSH,
		pb.ConnectionTypeRDP,
		pb.ConnectionTypeKubernetes,
	} {
		if user, pass := credentialsFor(subType.String()); user != "" || pass != "" {
			t.Errorf("%s: want no credentials, got %q/%q", subType, user, pass)
		}
	}
}

func TestCredentialsForUnknownSubtype(t *testing.T) {
	if user, pass := credentialsFor("not-a-subtype"); user != "" || pass != "" {
		t.Errorf("unknown subtype must report no credentials, got %q/%q", user, pass)
	}
}

// The agent's Oracle proxy re-keys the TNS handshake against the exact
// placeholder password the client typed (it passes "noop" as client_password),
// so changing these values silently breaks native Oracle clients — and every
// connection string already published to users.
func TestCredentialValuesAreStable(t *testing.T) {
	if credentialUser != "noop" || credentialPassword != "noop" {
		t.Fatalf("credentials changed to %q/%q; Oracle handshake re-keying and every "+
			"published connection string depend on noop/noop",
			credentialUser, credentialPassword)
	}
}
