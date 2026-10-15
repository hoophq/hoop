package sidecartui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

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

// Managed by hoop ends the screen with a login for the caller to run.
func TestChoosingManagedAsksTheCallerToLogIn(t *testing.T) {
	m := chooserModel(t)
	tm, cmd := m.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.boot == nil || !fm.boot.Login {
		t.Fatalf("boot = %+v, want a login", fm.boot)
	}
	if fm.boot.ConfigPath != "" || fm.boot.ControlPlaneURL != "" {
		t.Errorf("a login boot carries no config or plane: %+v", fm.boot)
	}
	if cmd == nil {
		t.Fatal("the screen did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the screen did not quit")
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
