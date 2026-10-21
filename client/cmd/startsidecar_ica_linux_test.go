//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// The Linux CLI links the ICA engine: a config with an ica lane validates
// here, where the darwin and windows CLIs refuse it by name. Validate
// loads no model and binds nothing, so this needs none of the engine's
// assets; without them the lane's notes say so, and the capabilities
// header leaves protocol:ica out — both pinned in sidecar/daemon.
func TestLinuxCLILinksTheICAEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := `listeners:
  - {name: vdi, protocol: ica, listen: 127.0.0.1:0, upstream: 10.0.0.1:2598, ica: {tenant_id: t}}
`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, det, err := daemon.SetupWith(path, configyaml.Load, buildSidecarPlugin)
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := daemon.Validate(loaded, det)
	if err != nil {
		t.Fatalf("the Linux CLI refused an ica lane, so the engine is not linked: %v", err)
	}
	if len(lanes) != 1 || lanes[0].Protocol != "ica" {
		t.Fatalf("lanes = %+v, want the one ica lane", lanes)
	}
}
