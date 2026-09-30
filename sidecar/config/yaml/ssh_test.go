package yaml_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/config/yaml"
)

// The transcode is generic, so an ssh block needs no yaml-side change. This
// pins that: the tri-state, the destination list and the identity mapping all
// have to survive YAML, because YAML is what an operator actually writes.
func TestSSHLaneLoadsFromYAML(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "host_key")
	ca := filepath.Join(dir, "ca.pub")
	for _, p := range []string{key, ca} {
		if err := os.WriteFile(p, []byte("material\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := yaml.LoadYAMLBytes([]byte(`
listeners:
  - name: prod-bastion
    protocol: ssh
    listen: 0.0.0.0:2222
    ssh:
      host_key: ` + key + `
      trusted_ca: ` + ca + `
      capabilities_allowed: []
      destinations_allowed:
        - 10.0.0.0/8:2222
      identity:
        subject: key_id
        groups: principals
`))
	if err != nil {
		t.Fatalf("an ssh lane was refused from YAML: %v", err)
	}

	l := cfg.Listeners[0]
	if l.SSH == nil {
		t.Fatal("the ssh block did not survive the transcode")
	}
	if l.SSH.CapabilitiesAllowed == nil {
		t.Error("an empty capabilities_allowed transcoded to absent; a bastion would get a shell")
	} else if len(*l.SSH.CapabilitiesAllowed) != 0 {
		t.Errorf("capabilities_allowed = %v, want empty", *l.SSH.CapabilitiesAllowed)
	}
	if got := l.SSH.DestinationsAllowed; len(got) != 1 || got[0] != "10.0.0.0/8:2222" {
		t.Errorf("destinations_allowed = %v", got)
	}
	if l.SSH.Identity == nil || l.SSH.Identity.Groups != "principals" {
		t.Errorf("identity mapping = %+v", l.SSH.Identity)
	}
}

// A key written with no value is the one spelling the tri-state cannot
// answer, and YAML is where it is easiest to write by accident.
func TestSSHEmptyCapabilitiesKeyIsRefusedFromYAML(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "host_key")
	if err := os.WriteFile(key, []byte("material\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := yaml.LoadYAMLBytes([]byte(`
listeners:
  - name: jump
    protocol: ssh
    listen: :2222
    ssh:
      host_key: ` + key + `
      trusted_ca: ` + key + `
      capabilities_allowed:
`))
	if err == nil {
		t.Fatal("capabilities_allowed with no value was accepted")
	}
	if !strings.Contains(err.Error(), "written with no value") {
		t.Errorf("error does not explain the tri-state: %v", err)
	}
}

// The relay block is what turns an ssh lane into a terminating bastion, so
// the whole of it has to survive YAML — the nested target map most of all,
// whose KEYS are hostnames and globs rather than field names.
func TestSSHRelayLoadsFromYAML(t *testing.T) {
	// REAL material, unlike the lanes above. The relay block parses what it
	// is pointed at — that is the whole point of checking it at load — so a
	// placeholder file would be refused here for the right reason and the
	// transcode would never be reached.
	dir := t.TempDir()
	key := writeRealPrivateKey(t, dir, "prod")
	kh := writeRealKnownHosts(t, dir, "db-01.prod:22")

	cfg, err := yaml.LoadYAMLBytes([]byte(`
listeners:
  - name: prod-bastion
    protocol: ssh
    listen: 0.0.0.0:2222
    ssh:
      host_key: ` + key + `
      trusted_ca: ` + key + `
      capabilities_allowed: [shell, pty, exec, env]
      destinations_allowed:
        - 10.0.0.0/8:22
      relay:
        known_hosts: ` + kh + `
        host_key_check: accept_new
        identities: ` + dir + `
        targets:
          "*.prod":
            private_key: ` + key + `
            capabilities_allowed: [exec, env]
          app-01.prod:
            agent_identity: true
          db-01.prod:
            capabilities_allowed: [exec, env, pty, shell, local_forward]
            forwards_allowed:
              - 127.0.0.1:5432
          legacy-01:
            private_key: ` + key + `
            login: ec2-user
`))
	if err != nil {
		t.Fatalf("a relay lane was refused from YAML: %v", err)
	}

	r := cfg.Listeners[0].SSH.Relay
	if r == nil {
		t.Fatal("the relay block did not survive the transcode")
	}
	if r.HostKeyCheck != "accept_new" || r.KnownHosts != kh || r.Identities != dir {
		t.Errorf("relay scalars = %+v", r)
	}
	if len(r.Targets) != 4 {
		t.Fatalf("targets = %d, want 4", len(r.Targets))
	}
	// A glob key is the fleet case, and it is the one a transcode is most
	// likely to mangle.
	glob := r.Targets["*.prod"]
	if glob == nil || glob.PrivateKey != key {
		t.Fatalf(`targets["*.prod"] = %+v`, glob)
	}
	if got := r.Targets["app-01.prod"]; got == nil || !got.AgentIdentity {
		t.Errorf("agent_identity did not survive: %+v", got)
	}
	db := r.Targets["db-01.prod"]
	if db == nil || db.CapabilitiesAllowed == nil {
		t.Fatalf(`targets["db-01.prod"] = %+v`, db)
	}
	if len(*db.CapabilitiesAllowed) != 5 {
		t.Errorf("target capabilities = %v", *db.CapabilitiesAllowed)
	}
	if got := db.ForwardsAllowed; len(got) != 1 || got[0] != "127.0.0.1:5432" {
		t.Errorf("forwards_allowed = %v", got)
	}
	if got := r.Targets["legacy-01"]; got == nil || got.Login != "ec2-user" {
		t.Errorf("login override did not survive: %+v", got)
	}
}

// A typo inside the relay block must not be silently dropped: it decides
// which hosts are inspected and what authenticates to them.
func TestSSHRelayUnknownKeyIsRefusedFromYAML(t *testing.T) {
	dir := t.TempDir()
	key := writeRealPrivateKey(t, dir, "prod")

	_, err := yaml.LoadYAMLBytes([]byte(`
listeners:
  - name: prod-bastion
    protocol: ssh
    listen: :2222
    ssh:
      host_key: ` + key + `
      trusted_ca: ` + key + `
      relay:
        known_hosts: ` + key + `
        hostkey_check: strict
        targets:
          db-01.prod:
            private_key: ` + key + `
`))
	if err == nil {
		t.Fatal("a misspelled key inside ssh.relay was accepted")
	}
	if !strings.Contains(err.Error(), "hostkey_check") {
		t.Errorf("error does not name the typo: %v", err)
	}
}

// writeRealPrivateKey and writeRealKnownHosts produce material the relay
// block's load-time checks accept. Standard library only: the sidecar has one
// dependency and a test file does not get to add a second.
func writeRealPrivateKey(t *testing.T, dir, name string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeRealKnownHosts(t *testing.T, dir string, hosts ...string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// An SSH public key on the wire is a sequence of length-prefixed
	// strings: the algorithm name, then the key itself.
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	encoded := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	for _, h := range hosts {
		b.WriteString(h + " ssh-ed25519 " + encoded + "\n")
	}
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
