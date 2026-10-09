package sidecartui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The token travels in the handshake's headers: a URL that would expose it
// is refused, wherever it came from.
func TestPlaneTransportError(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://cp.example.com":             true,
		"https://cp.example.com/prefix":      true,
		"http://localhost:8009":              true,
		"http://127.0.0.1:8009":              true,
		"http://[::1]:8009":                  true,
		"http://cp.example.com":              false,
		"HTTP://cp.example.com":              false,
		"http://localhost.evil.com":          false,
		"http://127.0.0.1.nip.io":            false,
		"http://0.0.0.0:8009":                false,
		"http://localhost@evil.com":          false,
		"https://user:secret@cp.example.com": false,
	} {
		err := PlaneTransportError(raw)
		if (err == nil) != ok {
			t.Errorf("PlaneTransportError(%q) = %v, want ok=%v", raw, err, ok)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("PlaneTransportError(%q) prints the password: %v", raw, err)
		}
	}
}

// A symlink in a cloned folder must not turn Replace into a write to the
// file it points at.
func TestReplaceFileRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(target, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hoop-sidecar.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(link, []byte("listeners: []\n"), 0o644); err == nil {
		t.Fatal("replaceFile wrote through a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep me\n" {
		t.Errorf("target = %q, want it untouched", b)
	}
	// The port fix too, on a target it could rewrite.
	cfg := sampleConfig("127.0.0.1:1")
	if err := os.WriteFile(target, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := usePorts(link, []portConflict{{portUse: portUse{addr: "127.0.0.1:1"}, free: "127.0.0.1:2"}}); err == nil {
		t.Error("usePorts accepted a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != cfg {
		t.Errorf("usePorts wrote through a symlink: target = %q", b)
	}
}

// replaceFile swaps the whole file and sets the mode it is given, and leaves
// no temporary file behind.
func TestReplaceFileReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "license.json")
	if err := os.WriteFile(path, []byte("old, longer content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(b) != "new\n" || fi.Mode().Perm() != 0o600 {
		t.Errorf("file = %q mode %v, want %q mode 0600", b, fi.Mode().Perm(), "new\n")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("folder holds %d entries, want only the file", len(entries))
	}
}

// A file whose content is a path is not a license: saving it would save
// the path.
func TestSaveLicenseRefusesAFileThatNamesAPath(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "other.json")
	outer := filepath.Join(dir, "pointer.txt")
	if err := os.WriteFile(outer, []byte(inner+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	save := filepath.Join(dir, "saved")
	if _, _, err := saveLicense(outer, save); err == nil || !strings.Contains(err.Error(), "does not hold a license document") {
		t.Fatalf("err = %v, want the file refused", err)
	}
	if _, err := os.Stat(filepath.Join(save, LicenseFile)); err == nil {
		t.Error("a license file was written")
	}
}

// File names come from the folder, which may be a cloned repository: a
// terminal sequence in one is drawn as text, never sent to the terminal.
func TestFileNamesAreCleanedBeforeTheyAreDrawn(t *testing.T) {
	dir := t.TempDir()
	name := "\x1b]52;c;aGk=\x07x.yaml"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("listeners: []\n"), 0o644); err != nil {
		t.Skipf("this filesystem refuses the name: %v", err)
	}
	p := newFilePicker(dir)
	if v := p.view(80, 20); strings.Contains(v, "\x1b]52") {
		t.Errorf("picker draws a raw OSC 52 sequence: %q", v)
	}
}
