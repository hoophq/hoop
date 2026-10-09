package sidecartui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func homeModel(t *testing.T, dir string, validate func(string) (string, error)) firstRunModel {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	m := newFirstRunModel("v1", func() time.Time { return now }, func() {}, noMachine, validate, dir)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	tm, _ = tm.Update(frReadyMsg{url: "http://127.0.0.1:15321"})
	return tm.(firstRunModel)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Get started is set up first, open a file second, then the folder's own
// configs, and it never shows the guide's link.
func TestGetStartedListsSetupOpenThenTheFolderConfigs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.yaml"), "x: 1\n")
	writeFile(t, filepath.Join(dir, "a.yml"), "x: 1\n")
	writeFile(t, filepath.Join(dir, ".hidden.yaml"), "x: 1\n")
	writeFile(t, filepath.Join(dir, "package.json"), "{}")
	m := homeModel(t, dir, validateFile)
	var ids []string
	for _, it := range m.home.items {
		ids = append(ids, it.id)
	}
	want := "setup,open,,file:" + filepath.Join(dir, "a.yml") + ",file:" + filepath.Join(dir, "b.yaml")
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("home = %s\nwant   %s", got, want)
	}
	out := ansi.Strip(m.render())
	if strings.Contains(out, "Open http://") || strings.Contains(out, "open the URL below") {
		t.Errorf("Get started still points at the guide's link:\n%s", out)
	}
	if !strings.Contains(out, "Set up a config file") || !strings.Contains(out, "Config files in this folder") {
		t.Errorf("Get started is not drawn:\n%s", out)
	}
}

func TestGetStartedSaysWhenTheFolderHasNoConfig(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	if out := ansi.Strip(m.render()); !strings.Contains(out, "No config in this directory") {
		t.Errorf("an empty folder is not called out:\n%s", out)
	}
}

// An invalid file keeps the person on the page with a plain sentence and
// the command that explains it, never the validator's raw text.
func TestChoosingAnInvalidConfigStaysAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	writeFile(t, path, "listeners: [")
	m := homeModel(t, dir, func(string) (string, error) {
		return "", errors.New("parse yaml: yaml: line 1: did not find expected node content")
	})
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "file:"+path {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, cmd := tm.Update(wkey("enter"))
	tm, quit := tm.Update(cmd())
	fm := tm.(firstRunModel)
	if fm.boot != nil || quit != nil {
		t.Fatal("an invalid config was booted")
	}
	out := ansi.Strip(fm.render())
	if !strings.Contains(out, "broken.yaml is not a valid sidecar config") || !strings.Contains(out, "--validate") {
		t.Errorf("the screen does not say the file is invalid:\n%s", out)
	}
	if strings.Contains(out, "did not find expected node") {
		t.Errorf("the raw validator error is on the screen:\n%s", out)
	}
	if fm.home.selected() != "file:"+path {
		t.Errorf("the cursor left the file, now on %q", fm.home.selected())
	}
}

func TestChoosingAValidConfigBootsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "good.yaml")
	writeFile(t, path, "x: 1\n")
	m := homeModel(t, dir, func(string) (string, error) { return "ok", nil })
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "file:"+path {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, cmd := tm.Update(wkey("enter"))
	tm, quit := tm.Update(cmd())
	if b := tm.(firstRunModel).boot; b == nil || b.ConfigPath != path || quit == nil {
		t.Fatalf("boot = %+v, quit = %v", b, quit != nil)
	}
}

// The picker walks folders: typing narrows, tab steps into a folder, and
// enter on a config opens it, which validates and boots.
func TestOpenAConfigFileByBrowsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy", "sidecar", "prod.yaml")
	writeFile(t, path, "x: 1\n")
	writeFile(t, filepath.Join(dir, "deploy", "notes.txt"), "no")
	m := homeModel(t, dir, func(string) (string, error) { return "ok", nil })
	var tm tea.Model = m
	tm, _ = tm.Update(wkey("o"))
	if tm.(firstRunModel).picker == nil {
		t.Fatal("o did not open the picker")
	}
	for _, k := range []string{"d", "e"} {
		tm, _ = tm.Update(wkey(k))
	}
	p := tm.(firstRunModel).picker
	if len(p.entries) != 1 || p.entries[0].name != "deploy" {
		t.Fatalf("typing narrowed to %+v, want deploy", p.entries)
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if names := entryNames(tm.(firstRunModel).picker); names != "..,sidecar" {
		t.Errorf("deploy/ lists %s, want ..,sidecar (notes.txt is not a config)", names)
	}
	for tm.(firstRunModel).picker.entries[tm.(firstRunModel).picker.cur].name != "sidecar" {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, _ = tm.Update(wkey("enter"))
	tm, cmd := tm.Update(wkey("enter"))
	if cmd == nil {
		t.Fatalf("enter on prod.yaml did not open it; picker lists %s", entryNames(tm.(firstRunModel).picker))
	}
	tm, _ = tm.Update(cmd())
	if b := tm.(firstRunModel).boot; b == nil || b.ConfigPath != path {
		t.Errorf("boot = %+v, want %s", b, path)
	}
}

func entryNames(p *filePicker) string {
	var out []string
	for _, e := range p.entries {
		out = append(out, e.name)
	}
	return strings.Join(out, ",")
}
