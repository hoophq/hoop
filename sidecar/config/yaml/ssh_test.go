package yaml_test

import (
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
