package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

)

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
