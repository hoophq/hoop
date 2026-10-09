package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// A JSON config that encoding/json reads but YAML refuses (a \/ escape)
// boots as it did before the demo key existed: it is never parsed as YAML.
func TestStartSidecarDemoLeavesAJSONConfigAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	doc := `{"listeners":[{"name":"api","protocol":"http","listen":"127.0.0.1:0","upstream":"http:\/\/127.0.0.1:1"}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := startSidecarDemo(path, &daemon.Config{})
	if err != nil {
		t.Fatalf("err = %v, want a JSON config left alone", err)
	}
	d.stop()
}

// A config file from the folder can name a plane. Checking it runs the
// handshake, so plain http to another machine is refused before the token
// in this shell is sent.
func TestValidateSidecarConfigRefusesAPlainRemotePlane(t *testing.T) {
	t.Setenv("HOOP_SIDECAR_TOKEN", "do-not-send")
	path := filepath.Join(t.TempDir(), "evil.yaml")
	doc := "control_plane_url: http://cp.example.com\nlisteners:\n  - name: api\n    protocol: http\n" +
		"    listen: 127.0.0.1:0\n    upstream: 127.0.0.1:1\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSidecarConfig(path); err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("err = %v, want the plain http plane refused", err)
	}
}

// The demo's curl commands are shown and copied to paste in a shell: a
// listener whose address is not a tcp loopback host:port never reaches them.
func TestStartSidecarDemoIgnoresAListenerItCannotQuote(t *testing.T) {
	addr := "127.0.0.1:18089"
	d, err := startSidecarDemo(writeDemoConfig(t, addr), &daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "sock", Protocol: "http", Network: "unix", Listen: "/tmp/a$(id)", Upstream: addr},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	if d.ports != nil {
		t.Errorf("tour ports = %+v, want no tour for a unix listener", d.ports)
	}
	for _, n := range d.notes {
		if strings.Contains(n, "$(id)") {
			t.Errorf("note carries the listener's shell text: %q", n)
		}
	}
}
