package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSSHKeyMaterial writes a real host key and a real trusted-CA file, so
// buildSSHServer is exercised against the same loading path a deployment
// uses rather than against a stub.
//
// Both are generated with the STANDARD LIBRARY. Reaching for
// golang.org/x/crypto/ssh here would make it a direct dependency of a module
// that has exactly one, even from a test file, so the two encodings are
// written out: PKCS#8 PEM for the private key, and the authorized_keys wire
// format for the public one.
func writeSSHKeyMaterial(t *testing.T) (hostKey, trustedCA string) {
	t.Helper()
	dir := t.TempDir()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	hostKey = filepath.Join(dir, "host_key")
	if err := os.WriteFile(hostKey, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	// An SSH public key on the wire is a sequence of length-prefixed
	// strings: the algorithm name, then the key itself.
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	trustedCA = filepath.Join(dir, "ca.pub")
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " hoop-ca\n"
	if err := os.WriteFile(trustedCA, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return hostKey, trustedCA
}

func buildSSHTestServer(t *testing.T, sc *SSHConfig) (SSHServer, error) {
	t.Helper()
	hostKey, trustedCA := writeSSHKeyMaterial(t)
	if sc.HostKey == "" {
		sc.HostKey = hostKey
	}
	if sc.TrustedCA == "" {
		sc.TrustedCA = trustedCA
	}
	return buildSSHServer(lane{
		cfg: ListenerConfig{
			Name:     "jump",
			Protocol: "ssh",
			Listen:   "127.0.0.1:0",
			SSH:      sc,
		},
		name: "jump",
	}, AuditConfig{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// An end-hop with no capability list admits the delivered set. This is the
// construction path, so it also proves the resolved set is a set libhoop
// accepts.
func TestSSHBuildAdmitsTheDeliveredSet(t *testing.T) {
	srv, err := buildSSHTestServer(t, &SSHConfig{})
	if err != nil {
		t.Fatalf("a default ssh lane did not build: %v", err)
	}
	defer srv.Close()

	notes := strings.Join(srv.Notes(), "\n")
	for _, want := range []string{"shell", "exec", "sftp"} {
		if !strings.Contains(notes, want) {
			t.Errorf("-validate notes do not mention %q:\n%s", want, notes)
		}
	}
}

// A bastion resolves to NO capabilities, and the key still has to cross the
// seam. libhoop refuses a missing capabilities setting on purpose: absent and
// empty would arrive as the same empty string, and a caller that forgot to
// resolve the tri-state would silently get the permissive reading.
func TestSSHBuildBastionSendsAnEmptyCapabilitySet(t *testing.T) {
	srv, err := buildSSHTestServer(t, &SSHConfig{
		CapabilitiesAllowed: &Capabilities{},
		DestinationsAllowed: []string{"10.0.0.0/8:2222"},
	})
	if err != nil {
		t.Fatalf("a bastion lane did not build: %v", err)
	}
	defer srv.Close()

	notes := strings.Join(srv.Notes(), "\n")
	if !strings.Contains(notes, "admits no session capability") {
		t.Errorf("a bastion does not say it carries no session:\n%s", notes)
	}
}

// The options map is libhoop's, and it refuses unknown keys. A lane that
// resolved its capabilities to nothing must still SET the key.
func TestSSHCapabilitiesOptionIsAlwaysPresent(t *testing.T) {
	if got := joinCapabilities(nil); got != "" {
		t.Errorf("an empty set rendered as %q", got)
	}
	sc := &SSHConfig{CapabilitiesAllowed: &Capabilities{"exec", "env"}}
	if got := joinCapabilities(sc.resolveCapabilities()); got != "exec,env" {
		t.Errorf("capabilities = %q", got)
	}
}

// Key material is loaded at BUILD, and -validate builds every lane. A bad
// path must not be discovered by the first client.
func TestSSHBuildRefusesUnparseableKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk")
	if err := os.WriteFile(junk, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildSSHTestServer(t, &SSHConfig{HostKey: junk}); err == nil {
		t.Error("a host key that does not parse was accepted at build")
	}
	if _, err := buildSSHTestServer(t, &SSHConfig{TrustedCA: junk}); err == nil {
		t.Error("a trusted CA that does not parse was accepted at build")
	}
}

// -validate has to state what an ssh lane will do, because the config file
// does not show it: an absent destination list looks like a complete bastion
// until the first forward is refused, and the account a session becomes is
// named in no key at all.
func TestSSHLaneNotesSayWhatTheLaneWillDo(t *testing.T) {
	notes := strings.Join(sshLaneNotes(&SSHConfig{}), "\n")
	if !strings.Contains(notes, "destinations_allowed is empty") {
		t.Errorf("an empty destination list is not reported:\n%s", notes)
	}
	if strings.Contains(notes, "carries nothing at all") {
		t.Errorf("an end-hop was described as carrying nothing:\n%s", notes)
	}
	if !strings.Contains(notes, "the login name the certificate admitted") {
		t.Errorf("the account a session becomes is not explained:\n%s", notes)
	}

	notes = strings.Join(sshLaneNotes(&SSHConfig{
		DestinationsAllowed: []string{"10.0.0.0/8:2222"},
	}), "\n")
	if !strings.Contains(notes, "10.0.0.0/8:2222") {
		t.Errorf("the destinations are not reported:\n%s", notes)
	}

	// The endpoint's own Notes carry the capability set, the CA, the host
	// key and the sftp account. Saying any of them here would print them
	// twice in one -validate run.
	caps := Capabilities{"exec", "sftp"}
	notes = strings.Join(sshLaneNotes(&SSHConfig{CapabilitiesAllowed: &caps}), "\n")
	if strings.Contains(notes, "file transfer") {
		t.Errorf("the lane repeated a note the endpoint already makes:\n%s", notes)
	}

	// A bastion spawns nothing, so a note about which account it would
	// become is noise on the one lane where it can never happen.
	notes = strings.Join(sshLaneNotes(&SSHConfig{
		CapabilitiesAllowed: &Capabilities{},
		DestinationsAllowed: []string{"any"},
	}), "\n")
	if strings.Contains(notes, "runs as the login name") {
		t.Errorf("a bastion was told which account a session becomes:\n%s", notes)
	}

	// A listener that admits no session AND carries no forward is
	// configured to do nothing. That is the one worth saying twice.
	notes = strings.Join(sshLaneNotes(&SSHConfig{CapabilitiesAllowed: &Capabilities{}}), "\n")
	if !strings.Contains(notes, "carries nothing at all") {
		t.Errorf("a listener that can do nothing does not say so:\n%s", notes)
	}
}
