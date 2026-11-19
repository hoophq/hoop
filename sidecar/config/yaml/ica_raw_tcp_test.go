package yaml_test

import (
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"os"
	"path/filepath"
	"testing"
)

func TestRawTCPExampleTranscodesAndValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "ica-sidecar", "examples", "config.raw-tcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configyaml.LoadYAMLBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ICA.RawTCP == nil || !cfg.ICA.RawTCP.Enable || cfg.ICA.DiagnosticRecording != nil || cfg.ICA.MaxSessions != 2 {
		t.Fatal("YAML raw configuration lost", cfg.ICA)
	}
}
