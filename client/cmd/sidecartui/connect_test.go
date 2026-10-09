package sidecartui

import (
	"errors"
	"fmt"
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

func focusOn(m firstRunModel, id string) {
	f := m.connect.form
	f.store()
	for i, x := range f.fields {
		if x.id == id {
			f.cur = i
		}
	}
	f.load()
}

func typeInto(t *testing.T, m firstRunModel, id, text string) firstRunModel {
	t.Helper()
	focusOn(m, id)
	m.connect.form.input.SetValue(text)
	m.connect.form.store()
	return m
}

func choose(m firstRunModel, how string) { m.connect.form.byID("how").text = how }

func press(t *testing.T, m firstRunModel, button string) (firstRunModel, tea.Cmd) {
	t.Helper()
	focusOn(m, button)
	tm, cmd := m.Update(wkey("enter"))
	return tm.(firstRunModel), cmd
}

// Connect sits right under Set up. Its page says what it unlocks and where
// to ask, starts on Talk to us, and offers the two ways as cards.
func TestConnectPageExplainsAndStartsOnTalkToUs(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	if m.home.items[1].id != "connect" {
		t.Fatalf("Connect is not under Set up: %+v", m.home.items[:2])
	}
	m = openConnect(t, m)
	if m.connect.form.focused().id != "meet" {
		t.Errorf("the page starts on %q, want Talk to us", m.connect.form.focused().id)
	}
	order := map[string]int{}
	for i, x := range m.connect.form.fields {
		order[x.id] = i
	}
	if !(order["continue"] > order["how"] && order["continue"] > order["token"] && order["continue"] > order["license"]) {
		t.Errorf("Continue is not below the cards and their fields: %v", order)
	}
	focusOn(m, "token")
	m.connect.form.update(wkey("enter"))
	if m.connect.form.focused().id != "continue" {
		t.Errorf("enter on the token goes to %q, want Continue", m.connect.form.focused().id)
	}
	focusOn(m, "meet")
	for _, gone := range []string{"connect", "savelicense"} {
		if m.connect.form.byID(gone) != nil {
			t.Errorf("the page still has a %q button", gone)
		}
	}
	flat := strings.Join(strings.Fields(ansi.Strip(m.render())), " ")
	for _, want := range []string{"ENTERPRISE", "unlimited AI analyzer, guardrails and data", "Talk to us", "Continue",
		"● Control Plane", "○ License", "Control Plane URL", "Sidecar token"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the page lacks %q:\n%s", want, flat)
		}
	}
	if strings.Contains(flat, "License {\"payload\"") {
		t.Error("the license field shows while Control Plane is chosen")
	}
	choose(m, unlockLicense)
	flat = strings.Join(strings.Fields(ansi.Strip(m.render())), " ")
	if !strings.Contains(flat, "● License") || strings.Contains(flat, "Sidecar token") {
		t.Errorf("choosing License does not swap the fields:\n%s", flat)
	}
}

func TestTalkToUsOpensTheMeetingPage(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	var opened string
	m.openURL = func(u string) error { opened = u; return nil }
	m = openConnect(t, m)
	tm, _ := m.Update(wkey("enter")) // Talk to us has the focus
	if opened != MeetURL || !strings.Contains(tm.(firstRunModel).connect.status, MeetURL) {
		t.Errorf("Talk to us opened %q", opened)
	}
}

// The URL and the token go together: either alone is refused with what is
// missing, and nothing is sent.
func TestPlaneURLAndTokenGoTogether(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	sent := false
	m.connectCheck = func(string, string) (*daemon.Config, error) { sent = true; return nil, nil }
	m = openConnect(t, m)
	for _, c := range []struct{ url, token, want string }{
		{"", "", "or choose License"},
		{"https://cp.example.com", "", "sidecar token too"},
		{"", "hsc_x", "Control Plane URL too"},
		{"cp.example.com", "hsc_x", "not a web address"},
		// The token would cross the network in the clear.
		{"http://cp.example.com", "hsc_x", "unencrypted"},
	} {
		m = typeInto(t, m, "url", c.url)
		m = typeInto(t, m, "token", c.token)
		var cmd tea.Cmd
		m, cmd = press(t, m, "continue")
		if cmd != nil || !m.connect.bad || !strings.Contains(m.connect.status, c.want) {
			t.Errorf("url %q token %q: status %q, want %q", c.url, c.token, m.connect.status, c.want)
		}
	}
	if sent {
		t.Error("an incomplete pair was sent to the plane")
	}
	// http stays for a plane on this machine.
	m = typeInto(t, m, "url", "http://127.0.0.1:8009")
	m = typeInto(t, m, "token", "hsc_x")
	if _, cmd := press(t, m, "continue"); cmd == nil {
		t.Errorf("a loopback http plane was refused: %q", m.connect.status)
	}
}

func continuePlane(t *testing.T, m firstRunModel, check func(string, string) (*daemon.Config, error)) (firstRunModel, tea.Cmd) {
	t.Helper()
	m.connectCheck = check
	m = openConnect(t, m)
	m = typeInto(t, m, "url", "https://cp.example.com/")
	m = typeInto(t, m, "token", "hsc_secret")
	if out := ansi.Strip(m.render()); strings.Contains(out, "hsc_secret") {
		t.Error("the token is shown on screen")
	}
	m, cmd := press(t, m, "continue")
	if cmd == nil {
		t.Fatal("Continue with a URL and a token did not check the plane")
	}
	tm, next := m.Update(cmd())
	return tm.(firstRunModel), next
}

func TestARefusedPlaneStaysOnThePage(t *testing.T) {
	m, next := continuePlane(t, homeModel(t, t.TempDir(), validateFile), func(string, string) (*daemon.Config, error) {
		return nil, errors.New("the control plane refused the token: 401\nmore")
	})
	if m.boot != nil || m.wiz != nil || next != nil {
		t.Fatal("a refused plane went on")
	}
	if m.connect.status != "✕ Could not connect: the control plane refused the token: 401" {
		t.Errorf("status = %q", m.connect.status)
	}
}

// A plane that already holds this sidecar's config boots on it: listeners
// set up here would be ignored.
func TestAPlaneWithAConfigBootsOnIt(t *testing.T) {
	m, quit := continuePlane(t, homeModel(t, t.TempDir(), validateFile), func(string, string) (*daemon.Config, error) {
		return &daemon.Config{}, nil
	})
	b := m.boot
	if b == nil || quit == nil || b.ControlPlaneURL != "https://cp.example.com" || b.Token != "hsc_secret" || b.ConfigPath != "" {
		t.Fatalf("boot = %+v", b)
	}
}

// A plane waiting for its first config leads on to setting one up, and the
// boot carries the plane with the saved file: the plane takes the file on
// that first handshake.
func TestAnEmptyPlaneLeadsToSetUpAndBootsWithIt(t *testing.T) {
	dir := t.TempDir()
	m, cmd := continuePlane(t, homeModel(t, dir, validateFile), func(string, string) (*daemon.Config, error) {
		return nil, fmt.Errorf("%w (at x); author one", daemon.ErrPlaneHasNoConfig)
	})
	if m.connect != nil || m.plane == nil || cmd == nil {
		t.Fatalf("an empty plane did not lead to Set up (connect %v, plane %v)", m.connect != nil, m.plane)
	}
	var tm tea.Model = m
	tm, _ = tm.Update(cmd()) // detected: the setup screens open
	fm := tm.(firstRunModel)
	if fm.wiz == nil || fm.wiz.plane != "https://cp.example.com" {
		t.Fatalf("the setup screens do not know the plane: %+v", fm.wiz)
	}
	tm, cmd = tm.Update(wkey("enter")) // the demo
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(wizValidatedMsg); !ok {
			break
		}
		tm, cmd = tm.Update(msg)
	}
	if out := ansi.Strip(tm.(firstRunModel).render()); !strings.Contains(strings.Join(strings.Fields(out), " "), "sends it to your Control Plane at cp.example.com") {
		t.Errorf("the overview does not say where the config goes:\n%s", out)
	}
	cmd = tm.(firstRunModel).wiz.save(true, false)
	tm, cmd = tm.Update(cmd())
	tm, quit := tm.Update(cmd())
	b := tm.(firstRunModel).boot
	if b == nil || quit == nil || b.ConfigPath != filepath.Join(dir, configyaml.StarterFile) ||
		b.ControlPlaneURL != "https://cp.example.com" || b.Token != "hsc_secret" {
		t.Fatalf("boot = %+v", b)
	}
}

// Leaving the setup forgets the plane: a file opened from the list later
// boots on its own, not on a connection made for another config.
func TestLeavingSetUpForgetsThePendingPlane(t *testing.T) {
	m, cmd := continuePlane(t, homeModel(t, t.TempDir(), validateFile), func(string, string) (*daemon.Config, error) {
		return nil, daemon.ErrPlaneHasNoConfig
	})
	var tm tea.Model = m
	tm, _ = tm.Update(cmd())
	tm, _ = tm.Update(wkey("esc"))
	if fm := tm.(firstRunModel); fm.wiz != nil || fm.plane != nil {
		t.Errorf("wiz %v, plane %v after leaving the setup", fm.wiz != nil, fm.plane)
	}
}

// Save only ends the setup too: the plane goes with it, so a file booted
// later from the list does not connect, or seed the plane, unasked.
func TestSaveOnlyForgetsThePendingPlane(t *testing.T) {
	dir := t.TempDir()
	m, cmd := continuePlane(t, homeModel(t, dir, validateFile), func(string, string) (*daemon.Config, error) {
		return nil, daemon.ErrPlaneHasNoConfig
	})
	var tm tea.Model = m
	tm, _ = tm.Update(cmd())
	tm, _ = tm.Update(wkey("enter")) // the demo
	fm := tm.(firstRunModel)
	tm, _ = tm.Update(fm.wiz.save(false, false)())
	fm = tm.(firstRunModel)
	if fm.wiz != nil || fm.saved == "" {
		t.Fatalf("Save only did not save and return home (wiz %v, saved %q, err %v)", fm.wiz != nil, fm.saved, fm.wiz)
	}
	if fm.plane != nil {
		t.Errorf("the pending plane %+v outlived Save only", fm.plane)
	}
}

// License: checked before it is written, saved readable by its owner only,
// then on to setting up a config that names it.
func TestContinueWithALicense(t *testing.T) {
	dir := t.TempDir()
	m := homeModel(t, dir, validateFile)
	m.licenseDir = filepath.Join(dir, "license")
	var used string
	m.useLicense = func(p string) { used = p }
	m = openConnect(t, m)
	choose(m, unlockLicense)
	m, cmd := press(t, m, "continue")
	if cmd != nil || !strings.Contains(m.connect.status, "paste your license") {
		t.Fatalf("an empty license: status %q", m.connect.status)
	}
	m = typeInto(t, m, "license", licensetest.Document(t, licensetest.Expiring(-time.Hour)))
	m, _ = press(t, m, "continue")
	if !strings.Contains(m.connect.status, "expired") || used != "" {
		t.Fatalf("an expired license: status %q, used %q", m.connect.status, used)
	}
	m = typeInto(t, m, "license", licensetest.Document(t, licensetest.Enterprise()))
	m, cmd = press(t, m, "continue")
	if cmd == nil || m.connect != nil {
		t.Fatalf("a valid license did not continue: status %q", m.connect.status)
	}
	if used != filepath.Join(dir, "license", LicenseFile) {
		t.Errorf("license saved at %q", used)
	}
	if fi, err := os.Stat(used); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("license file mode %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	var tm tea.Model = m
	tm, _ = tm.Update(cmd())
	tm, _ = tm.Update(wkey("enter")) // the demo
	cfg, opts, err := tm.(firstRunModel).wiz.d.config()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := configyaml.Render(cfg, opts)
	if cfg.License != used || !strings.Contains(string(b), "license: "+used) {
		t.Errorf("the config does not name the license:\n%s", b)
	}
}

func TestSaveLicense(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := saveLicense(`{"payload":{}}`, dir); err == nil || !strings.Contains(err.Error(), "not a valid hoop license") {
		t.Errorf("forged: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LicenseFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused license was written")
	}
	doc := licensetest.Document(t, licensetest.Enterprise())
	other := filepath.Join(t.TempDir(), "mine.json")
	writeFile(t, other, doc)
	if _, st, err := saveLicense(other, dir); err != nil || !strings.Contains(licenseLine(st), "valid until") {
		t.Errorf("from a path: %v", err)
	}
}
