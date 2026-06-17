package daemonconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	// A fresh install has no config.toml yet. The daemon should boot
	// the IPC layer anyway so the operator can run `hsh login`.
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg.APIURL != "" || cfg.Token != "" || cfg.LoggedIn() {
		t.Errorf("empty config has values: %+v", cfg)
	}
}

func TestLoad_BadPath(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load with empty path should fail")
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := Config{
		APIURL:   "https://hoop.example.com",
		GrpcURL:  "grpcs://hoop.example.com:8443",
		Token:    "secret-bearer-token-value",
		LogLevel: "debug",
	}
	if err := Save(path, original, SaveOptions{Mode: 0o600, DirMode: 0o700}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != original {
		t.Errorf("roundtrip mismatch:\n  saved: %+v\n  loaded: %+v", original, got)
	}
}

func TestSave_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	first := Config{APIURL: "https://first.example.com", Token: "tok1"}
	if err := Save(path, first, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	second := Config{APIURL: "https://second.example.com", Token: "tok2"}
	if err := Save(path, second, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != second {
		t.Errorf("second save did not stick: %+v", got)
	}

	// No leftover .tmp files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestSave_AppliesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Save(path, Config{APIURL: "x"}, SaveOptions{Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

func TestLoad_RejectsCorruptTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not valid TOML = ===="), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject malformed TOML")
	}
}

func TestLoggedIn(t *testing.T) {
	if (Config{}).LoggedIn() {
		t.Error("empty config reports LoggedIn")
	}
	if !(Config{Token: "x"}).LoggedIn() {
		t.Error("config with token does not report LoggedIn")
	}
}

func TestSave_EmptyOptionalFieldsOmitted(t *testing.T) {
	// `omitempty` should mean a config with only APIURL produces a
	// minimal file. Operators editing the file by hand benefit from
	// not seeing empty `grpc_url = ""` etc. lines.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Save(path, Config{APIURL: "https://x.example.com"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	body := string(raw)
	if !strings.Contains(body, "api_url") {
		t.Errorf("api_url missing from output:\n%s", body)
	}
	if strings.Contains(body, "token") {
		t.Errorf("empty token should be omitted:\n%s", body)
	}
	if strings.Contains(body, "grpc_url") {
		t.Errorf("empty grpc_url should be omitted:\n%s", body)
	}
}

func TestDefaultConfigPathPlatform_PosixVsWindows(t *testing.T) {
	// Sanity check: the function returns the documented platform
	// default for whichever OS the test runs on. We don't try to fake
	// runtime.GOOS — instead we just assert the result has the
	// expected suffix.
	got := DefaultConfigPathPlatform()
	if got == "" {
		t.Fatal("DefaultConfigPathPlatform returned empty")
	}
	if !strings.HasSuffix(got, "config.toml") {
		t.Errorf("path = %q, want a config.toml suffix", got)
	}
}
