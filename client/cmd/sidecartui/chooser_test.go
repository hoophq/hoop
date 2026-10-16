package sidecartui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// Configured in the web app while the screen waits: the plane check finds a
// configuration, and the screen boots on it, as Save and boot would.
func TestAConfigurationSetInHoopBootsTheConnectedScreen(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	m.managed = &testPlane
	m.buildHome()
	ready := false
	m.connectCheck = func(url, token string) (*daemon.Config, error) {
		if url != testPlane.URL || token != testPlane.Token {
			t.Errorf("checked %s with %s", url, token)
		}
		if !ready {
			return nil, daemon.ErrPlaneHasNoConfig
		}
		return &daemon.Config{}, nil
	}

	// Nothing in hoop yet: it keeps asking, and boots nothing.
	tm, cmd := m.Update(frProbeTickMsg{})
	tm, next := tm.Update(cmd())
	if tm.(firstRunModel).boot != nil || next == nil {
		t.Fatalf("an empty plane booted, or the screen stopped asking")
	}

	// The web app set its listener: the next check boots on the plane.
	ready = true
	tm, cmd = tm.Update(frProbeTickMsg{})
	tm, quit := tm.Update(cmd())
	fm := tm.(firstRunModel)
	if fm.boot == nil || fm.boot.ConfigPath != "" || fm.boot.ControlPlaneURL != testPlane.URL || fm.boot.Token != testPlane.Token {
		t.Fatalf("boot = %+v, want the plane and no file", fm.boot)
	}
	if !fm.planeReady {
		t.Error("the screen does not know the plane holds the configuration")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Error("the screen did not quit to boot")
	}
}

// Without a login nothing is asked.
func TestOnlyAConnectedScreenAsksThePlane(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	m.connectCheck = func(string, string) (*daemon.Config, error) {
		t.Error("asked a plane without a login")
		return nil, nil
	}
	if _, cmd := m.Update(frProbeTickMsg{}); cmd != nil {
		t.Error("a screen with no login keeps asking")
	}
}

// chooserModel is the first-run screen offering the hoop-managed plane.
func chooserModel(t *testing.T) firstRunModel {
	t.Helper()
	m := homeModel(t, t.TempDir(), validateFile)
	m.managedURL, m.choosing = "https://use.hoop.dev", true
	m.buildHome()
	m.width, m.height = 120, 40
	return m
}

// The first thing a new sidecar asks: hoop-managed or Enterprise, and
// nothing else on the list.
func TestTheFirstScreenAsksManagedOrEnterprise(t *testing.T) {
	m := chooserModel(t)
	var ids []string
	for _, it := range m.home.items {
		ids = append(ids, it.id)
	}
	if strings.Join(ids, ",") != "managed,enterprise" {
		t.Fatalf("the choices are %v", ids)
	}
	screen := ansi.Strip(m.render())
	for _, want := range []string{"How do you want to run this sidecar?", "Managed by hoop (free)", "use.hoop.dev", "Enterprise"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen does not show %q", want)
		}
	}
}

// fakeLogin is a ManagedLogin that answers at once.
type fakeLogin struct {
	ticket  LoginTicket
	plane   ManagedPlane
	waitErr error
}

func (f fakeLogin) Begin(context.Context) (LoginTicket, error) { return f.ticket, nil }
func (f fakeLogin) Wait(context.Context) (ManagedPlane, error) { return f.plane, f.waitErr }

var testPlane = ManagedPlane{URL: "https://use.hoop.dev", Token: "hsc_x", Name: "edge-1",
	Page: "https://use.hoop.dev/sidecars/id-1"}

// run feeds cmd's message back into the model, as the program would.
func run(t *testing.T, m firstRunModel, cmd tea.Cmd) firstRunModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	tm, next := m.Update(cmd())
	fm := tm.(firstRunModel)
	if next != nil {
		if msg := next(); msg != nil {
			if _, quit := msg.(tea.QuitMsg); !quit {
				tm, _ = fm.Update(msg)
				fm = tm.(firstRunModel)
			}
		}
	}
	return fm
}

// Managed by hoop logs in on the screen: the link and the code, then, once
// approved, the setup list, connected. The screen never leaves for it.
func TestManagedLogsInOnTheScreenThenSetsUpHere(t *testing.T) {
	m := chooserModel(t)
	var opened []string
	m.openURL = func(u string) error { opened = append(opened, u); return nil }
	m.managedLogin = fakeLogin{
		ticket: LoginTicket{URL: "https://use.hoop.dev/sidecars/login?code=BCDF-GHJK", Code: "BCDF-GHJK"},
		plane:  testPlane,
	}
	tm, cmd := m.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.login == nil || fm.boot != nil {
		t.Fatalf("Managed did not open the login page (login=%v boot=%+v)", fm.login != nil, fm.boot)
	}

	// The ticket arrives: shown, and the browser opened on it.
	tm, waitCmd := fm.Update(cmd())
	fm = tm.(firstRunModel)
	screen := ansi.Strip(fm.render())
	for _, want := range []string{"Approve this sidecar in your browser", "login?code=BCDF-GHJK", "BCDF-GHJK", "Approve only if it matches"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the login page does not show %q", want)
		}
	}
	if len(opened) != 1 || !strings.Contains(opened[0], "BCDF-GHJK") {
		t.Errorf("opened %v", opened)
	}

	// Approved: back on the setup list, connected, with hoop's own way last.
	tm, _ = fm.Update(waitCmd())
	fm = tm.(firstRunModel)
	if fm.login != nil || fm.managed == nil || fm.choosing {
		t.Fatalf("not connected after approval (login=%v managed=%v choosing=%v)", fm.login != nil, fm.managed, fm.choosing)
	}
	var ids []string
	for _, it := range fm.home.items {
		if !it.note {
			ids = append(ids, it.id)
		}
	}
	if ids[0] != "setup" || ids[len(ids)-1] != "hoopconfig" {
		t.Errorf("connected choices %v: setup first, hoop last", ids)
	}
	for _, id := range ids {
		if id == "connect" {
			t.Error("a logged-in sidecar still offers Connect to a Control Plane")
		}
	}
	screen = ansi.Strip(fm.render())
	for _, want := range []string{"Finish the setup here", "Connected to hoop as edge-1", "Configure in hoop instead"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the connected screen does not show %q", want)
		}
	}
}

// Every boot from the connected screen stays connected: a file set up or
// opened here boots with the login, and the setup leaving keeps it.
func TestAConnectedScreenBootsTheFileConnected(t *testing.T) {
	dir := t.TempDir()
	m := homeModel(t, dir, validateFile)
	m.managed = &testPlane
	m.buildHome()
	m.checking = "/tmp/x.yaml"
	tm, _ := m.Update(frCheckedMsg{path: "/tmp/x.yaml"})
	fm := tm.(firstRunModel)
	if fm.boot == nil || fm.boot.ConfigPath != "/tmp/x.yaml" ||
		fm.boot.ControlPlaneURL != testPlane.URL || fm.boot.Token != testPlane.Token {
		t.Fatalf("boot = %+v, want the file on the managed plane", fm.boot)
	}
}

// Configure in hoop instead boots with no file and opens the sidecar's page.
func TestConfigureInHoopBootsWithNoFile(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	var opened []string
	m.openURL = func(u string) error { opened = append(opened, u); return nil }
	m.managed = &testPlane
	m.buildHome()
	for m.home.selected() != "hoopconfig" {
		tm, _ := m.Update(wkey("down"))
		m = tm.(firstRunModel)
	}
	tm, cmd := m.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.boot == nil || fm.boot.ConfigPath != "" || fm.boot.ControlPlaneURL != testPlane.URL {
		t.Fatalf("boot = %+v, want the plane and no file", fm.boot)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the screen did not quit")
	}
	if len(opened) != 1 || opened[0] != testPlane.Page {
		t.Errorf("opened %v, want the sidecar page", opened)
	}
}

// esc leaves the login for the choice, and a late answer changes nothing.
func TestEscLeavesTheLogin(t *testing.T) {
	m := chooserModel(t)
	m.managedLogin = fakeLogin{ticket: LoginTicket{URL: "u", Code: "BCDF-GHJK"}, plane: testPlane}
	tm, cmd := m.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	tm, _ = fm.Update(wkey("esc"))
	fm = tm.(firstRunModel)
	if fm.login != nil || !fm.choosing {
		t.Fatalf("esc did not return to the choice")
	}
	tm, _ = fm.Update(cmd())
	tm, _ = tm.Update(frLoggedInMsg{plane: testPlane})
	if tm.(firstRunModel).managed != nil {
		t.Error("an answer after esc logged the sidecar in")
	}
}

// A denied login says so on the page.
func TestADeniedLoginShowsWhy(t *testing.T) {
	m := chooserModel(t)
	m.managedLogin = fakeLogin{ticket: LoginTicket{URL: "u", Code: "BCDF-GHJK"},
		waitErr: errors.New("the sidecar login was denied in the web app")}
	tm, cmd := m.Update(wkey("enter"))
	fm := run(t, tm.(firstRunModel), cmd)
	if fm.login == nil || !strings.Contains(fm.login.err, "denied") {
		t.Fatalf("login = %+v", fm.login)
	}
	if !strings.Contains(ansi.Strip(fm.render()), "denied") {
		t.Error("the page does not say the login was denied")
	}
}

// Enterprise opens the Connect page, and back from it is the first screen.
func TestChoosingEnterpriseOpensConnectAndBackReturns(t *testing.T) {
	m := chooserModel(t)
	tm, _ := m.Update(wkey("down"))
	tm, _ = tm.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.connect == nil || fm.choosing {
		t.Fatalf("Enterprise did not open the Connect page (connect=%v choosing=%v)", fm.connect != nil, fm.choosing)
	}
	if fm.boot != nil {
		t.Fatalf("Enterprise booted: %+v", fm.boot)
	}
	fm, _ = press(t, fm, "back")
	if fm.connect != nil || !fm.choosing || fm.home.items[0].id != "managed" {
		t.Fatalf("back did not return to the first screen")
	}
}

// q leaves without a boot.
func TestQuittingTheChooserBootsNothing(t *testing.T) {
	m := chooserModel(t)
	stopped := false
	m.stop = func() { stopped = true }
	tm, _ := m.Update(wkey("q"))
	fm := tm.(firstRunModel)
	if fm.boot != nil || !stopped {
		t.Fatalf("boot=%+v stopped=%v", fm.boot, stopped)
	}
}

// Without a managed plane the screen is Get started, as before.
func TestNoManagedPlaneKeepsGetStarted(t *testing.T) {
	m := homeModel(t, t.TempDir(), validateFile)
	m.width, m.height = 120, 40
	if m.choosing || m.home.items[0].id != "setup" {
		t.Fatalf("first item %q, choosing=%v", m.home.items[0].id, m.choosing)
	}
}
