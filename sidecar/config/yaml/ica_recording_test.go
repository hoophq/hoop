package yaml_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
)

func TestDiagnosticRecordingExampleTranscodesAndValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "ica-sidecar", "examples", "config.diagnostic-recording.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = configyaml.LoadYAMLBytes(data); err == nil {
		t.Fatal("unreviewed example enabled recording")
	}
	until := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	data = []byte(strings.ReplaceAll(string(data), "REPLACE-WITH-ABSOLUTE-RFC3339-EXPIRY", until))
	cfg, err := configyaml.LoadYAMLBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Admin.ICAReadinessJSON || cfg.ICA.DiagnosticRecording == nil || cfg.ICA.DiagnosticRecording.Until != until || !cfg.ICA.DiagnosticRecording.AcknowledgeUnredacted {
		t.Fatal("YAML recording config lost", cfg.ICA)
	}
	data = []byte(strings.ReplaceAll(string(data), "acknowledge_unredacted: true", "acknowledge_unredacted: false"))
	if _, err = configyaml.LoadYAMLBytes(data); err == nil {
		t.Fatal("YAML enabled unacknowledged recording")
	}
}
