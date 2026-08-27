package analyzer_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/hoopinspect/analyzer"
)

const material = "sk-super-secret-key-value"

// Every rendering path must refuse. These are the four ways a credential
// escapes in practice: a struct dump, a verbose struct dump, a debug endpoint
// marshalling something that transitively holds it, and a log line.
func TestSecretNeverRenders(t *testing.T) {
	s := analyzer.NewSecret([]byte(material))

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"String", s.String()},
		{"%v", fmt.Sprintf("%v", s)},
		{"%s", fmt.Sprintf("%s", s)},
		{"%#v", fmt.Sprintf("%#v", s)},
		{"%+v in a struct", fmt.Sprintf("%+v", struct {
			Key analyzer.Secret
		}{s})},
	} {
		if strings.Contains(tc.got, material) {
			t.Errorf("%s leaked the credential: %q", tc.name, tc.got)
		}
		if !strings.Contains(tc.got, "REDACTED") {
			t.Errorf("%s = %q, want a redaction marker", tc.name, tc.got)
		}
	}
}

// A Secret reachable from anything an admin endpoint serializes must emit the
// placeholder, not the material.
func TestSecretMarshalJSON(t *testing.T) {
	payload := struct {
		Key analyzer.Secret `json:"key"`
	}{analyzer.NewSecret([]byte(material))}

	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if bytes.Contains(out, []byte(material)) {
		t.Errorf("json.Marshal leaked the credential: %s", out)
	}
}

// Structured logging is the path a plain string escapes through most quietly:
// somebody logs the config and the key lands in the log pipeline.
func TestSecretLogValue(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("config", "key", analyzer.NewSecret([]byte(material)))

	if strings.Contains(buf.String(), material) {
		t.Errorf("slog leaked the credential: %s", buf.String())
	}
}

// Bytes is the one deliberate exit, and it must still work: the provider has
// to build an Authorization header.
func TestSecretBytesReturnsMaterial(t *testing.T) {
	s := analyzer.NewSecret([]byte(material))
	if string(s.Bytes()) != material {
		t.Errorf("Bytes() = %q, want the material", s.Bytes())
	}
}

// An unset Secret renders distinctly, because "no credential configured" and
// "a credential we refuse to print" are different operator problems.
func TestZeroSecretRendersUnset(t *testing.T) {
	var s analyzer.Secret
	if !s.IsZero() {
		t.Error("the zero Secret does not report itself empty")
	}
	if got := s.String(); got != "<unset>" {
		t.Errorf("String() = %q, want <unset>", got)
	}
}

// A credential anyone on the host can read is already disclosed. Failing at
// startup is the only moment an operator acts on it.
func TestReadSecretFileRefusesLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte(material), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := analyzer.ReadSecretFile(path)
	if err == nil {
		t.Fatal("a world-readable credential file was accepted")
	}
	if !strings.Contains(err.Error(), "0644") {
		t.Errorf("the error does not report the mode: %v", err)
	}
	if strings.Contains(err.Error(), material) {
		t.Errorf("the error leaked the credential: %v", err)
	}
}

func TestReadSecretFileAcceptsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	// A trailing newline is what a credential pasted into a file always
	// carries, and an API key with \n appended fails auth with a message
	// naming neither the newline nor the file.
	if err := os.WriteFile(path, []byte(material+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := analyzer.ReadSecretFile(path)
	if err != nil {
		t.Fatalf("ReadSecretFile: %v", err)
	}
	if string(s.Bytes()) != material {
		t.Errorf("Bytes() = %q, want the trimmed material", s.Bytes())
	}
}

func TestReadSecretFileMissing(t *testing.T) {
	if _, err := analyzer.ReadSecretFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing credential file was accepted")
	}
}
