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

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

func openConnect(t *testing.T, m firstRunModel) firstRunModel {
	t.Helper()
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "connect" {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, _ = tm.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.connect == nil {
		t.Fatal("enter on Connect to a Control Plane did not open the page")
	}
	return fm
}

func typeInto(t *testing.T, m firstRunModel, id, text string) firstRunModel {
	t.Helper()
	f := m.connect.form
	f.store()
	for i, x := range f.fields {
		if x.id == id {
			f.cur = i
		}
	}
	f.load()
	f.input.SetValue(text)
	f.store()
	return m
}

func press(t *testing.T, m firstRunModel, button string) (firstRunModel, tea.Cmd) {
	t.Helper()
	f := m.connect.form
	f.store()
	for i, x := range f.fields {
		if x.id == button {
			f.cur = i
		}
	}
	f.load()
	tm, cmd := m.Update(wkey("enter"))
	return tm.(firstRunModel), cmd
}

// Connect sits right under Set up, and its page says what it is for and
// where to ask, with the URL field ready to type in.
func TestConnectPageExplainsAndStartsOnTheURL(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	if m.home.items[1].id != "connect" {
		t.Fatalf("Connect is not under Set up: %+v", m.home.items[:2])
	}
	m = openConnect(t, m)
	if m.connect.form.focused().id != "url" {
		t.Errorf("the page starts on %q, want the URL field", m.connect.form.focused().id)
	}
	flat := strings.Join(strings.Fields(ansi.Strip(m.render())), " ")
	for _, want := range []string{"ENTERPRISE", "unlimited AI analyzer, guardrails and data", "masking, reviews in the web app", "Talk to us", "https://hoop.example.com",
		"Control Plane URL", "Sidecar token", "OR START WITH A LICENSE", "Save license", "Connect and boot"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the page lacks %q:\n%s", want, flat)
		}
	}
}

func TestTalkToUsOpensTheMeetingPage(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	var opened string
	m.openURL = func(u string) error { opened = u; return nil }
	m = openConnect(t, m)
	m, _ = press(t, m, "meet")
	if opened != MeetURL {
		t.Errorf("Talk to us opened %q, want %s", opened, MeetURL)
	}
}

// A wrong address, a missing token, or a plane that refuses: each is a line
// on the page, and nothing boots.
func TestConnectRefusesBadInputAndReportsAFailedHandshake(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	m.connectCheck = func(string, string) (*daemon.Config, error) {
		return nil, errors.New("control plane refused the token: 401 unauthorized\nmore detail")
	}
	m = openConnect(t, m)
	m, cmd := press(t, m, "connect")
	if cmd != nil || !m.connect.bad || !strings.Contains(m.connect.status, "URL") {
		t.Fatalf("empty fields: status %q", m.connect.status)
	}
	m = typeInto(t, m, "url", "cp.example.com")
	m = typeInto(t, m, "token", "hsc_x")
	m, _ = press(t, m, "connect")
	if !strings.Contains(m.connect.status, "not a web address") {
		t.Errorf("a URL with no scheme: status %q", m.connect.status)
	}
	m = typeInto(t, m, "url", "https://cp.example.com")
	m, cmd = press(t, m, "connect")
	if cmd == nil {
		t.Fatal("a valid URL and token did not start the check")
	}
	tm, quit := m.Update(cmd())
	fm := tm.(firstRunModel)
	if fm.boot != nil || quit != nil {
		t.Fatal("a refused handshake booted")
	}
	if fm.connect.status != "✕ Could not connect: control plane refused the token: 401 unauthorized" {
		t.Errorf("status = %q", fm.connect.status)
	}
}

func TestConnectBootsOnTheControlPlane(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	var gotURL, gotToken string
	m.connectCheck = func(u, tok string) (*daemon.Config, error) {
		gotURL, gotToken = u, tok
		return &daemon.Config{}, nil
	}
	m = openConnect(t, m)
	m = typeInto(t, m, "url", "https://cp.example.com/")
	m = typeInto(t, m, "token", "hsc_secret")
	if out := ansi.Strip(m.render()); strings.Contains(out, "hsc_secret") {
		t.Error("the token is shown on screen")
	}
	m, cmd := press(t, m, "connect")
	tm, quit := m.Update(cmd())
	b := tm.(firstRunModel).boot
	if b == nil || quit == nil || b.ControlPlaneURL != "https://cp.example.com" || b.Token != "hsc_secret" || b.ConfigPath != "" {
		t.Fatalf("boot = %+v", b)
	}
	if gotURL != "https://cp.example.com" || gotToken != "hsc_secret" {
		t.Errorf("checked %q %q", gotURL, gotToken)
	}
}

// A license is checked before it is written: a forged or expired one is
// refused with a plain sentence and nothing lands on disk; a valid one is
// saved readable by its owner only, and handed to the CLI.
func TestSaveLicense(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := saveLicense(`{"payload":{}}`, dir); err == nil || !strings.Contains(err.Error(), "not a valid hoop license") {
		t.Errorf("forged: err = %v", err)
	}
	if _, _, err := saveLicense(licensetest.Document(t, licensetest.Expiring(-time.Hour)), dir); err == nil ||
		!strings.Contains(err.Error(), "expired") {
		t.Errorf("expired: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LicenseFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused license was written")
	}
	doc := licensetest.Document(t, licensetest.Enterprise())
	path, st, err := saveLicense(doc, dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("saved %s with mode %v, want 0600 (%v)", path, fi.Mode().Perm(), err)
	}
	if !strings.Contains(licenseLine(st), "valid until") {
		t.Errorf("licenseLine = %q", licenseLine(st))
	}
	// The path of a license file works as well as the document.
	other := filepath.Join(t.TempDir(), "mine.json")
	writeFile(t, other, doc)
	if _, _, err := saveLicense(other, t.TempDir()); err != nil {
		t.Errorf("from a path: %v", err)
	}
}

// Saving on the page hands the license to the CLI and to every config set
// up afterwards, under its license key.
func TestSavedLicenseReachesTheConfigsSetUpAfter(t *testing.T) {
	dir := t.TempDir()
	m := homeModel(t, dir, validateFile)
	m.licenseDir = filepath.Join(dir, "keys")
	var used string
	m.useLicense = func(p string) { used = p }
	m = openConnect(t, m)
	m = typeInto(t, m, "license", licensetest.Document(t, licensetest.Enterprise()))
	m, _ = press(t, m, "savelicense")
	if m.connect.bad || used == "" || m.licensePath != used {
		t.Fatalf("status %q, used %q", m.connect.status, used)
	}
	m, _ = press(t, m, "back")
	if !strings.Contains(ansi.Strip(m.render()), "Running under your license") {
		t.Error("the home screen does not say a license is in use")
	}
	var tm tea.Model = m
	tm, cmd := tm.Update(wkey("w"))
	tm, _ = tm.Update(cmd())
	tm, _ = tm.Update(wkey("enter")) // the demo
	fm := tm.(firstRunModel)
	cfg, opts, err := fm.wiz.d.config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.License != used {
		t.Errorf("config license = %q, want %q", cfg.License, used)
	}
	b, _ := configyaml.Render(cfg, opts)
	if !strings.Contains(string(b), "license: "+used) {
		t.Errorf("the written config has no license key:\n%s", b)
	}
}
