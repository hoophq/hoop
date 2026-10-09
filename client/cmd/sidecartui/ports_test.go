package sidecartui

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
)

// sampleConfig is the smallest config that loads, listening on listen.
func sampleConfig(listen string) string {
	return "# my notes stay\nlisteners:\n  - name: api   # the one\n    protocol: http\n    listen: " + listen +
		"   # keep me\n    upstream: 127.0.0.1:1\n"
}

// hold takes a free loopback port and keeps it until the test ends, the way
// another program would.
func hold(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func TestPortConflictsFindsATakenPortAndOffersAFreeOne(t *testing.T) {
	busy := hold(t)
	path := filepath.Join(t.TempDir(), "c.yaml")
	writeFile(t, path, sampleConfig(busy))
	got, err := portConflicts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].addr != busy || !got[0].inUse || got[0].what != "listener api" {
		t.Fatalf("conflicts = %+v", got)
	}
	if got[0].free == "" || got[0].free == busy || bindError(got[0].free) != nil {
		t.Errorf("offered %q, want a free address other than %s", got[0].free, busy)
	}
	writeFile(t, path, sampleConfig("127.0.0.1:0"))
	if got, _ := portConflicts(path); len(got) != 0 {
		t.Errorf("a free port was reported taken: %+v", got)
	}
}

// Using the free port changes the address and nothing else: the person's
// comments, layout and other values survive.
func TestUsePortsChangesOnlyTheAddress(t *testing.T) {
	busy := hold(t)
	path := filepath.Join(t.TempDir(), "c.yaml")
	writeFile(t, path, sampleConfig(busy))
	c := []portConflict{{portUse: portUse{what: "listener api", addr: busy}, inUse: true, free: "127.0.0.1:1234"}}
	if err := usePorts(path, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if want := sampleConfig("127.0.0.1:1234"); string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
}

// Only a listen key's value moves: the same text inside a block scalar is
// the person's prose, and stays word for word.
func TestUsePortsLeavesABlockScalarAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	body := "listeners:\n  - name: api\n    protocol: http\n    listen: 127.0.0.1:15432\n    upstream: 127.0.0.1:1\n" +
		"guardrails:\n  rules:\n    - name: r\n      type: deny_words_list\n      words: [drop]\n      message: |\n" +
		"        Use the sidecar.\n        listen: 127.0.0.1:15432\n"
	writeFile(t, path, body)
	c := []portConflict{{portUse: portUse{what: "listener api", addr: "127.0.0.1:15432"}, free: "127.0.0.1:15433"}}
	if err := usePorts(path, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := strings.Replace(body, "    listen: 127.0.0.1:15432\n", "    listen: 127.0.0.1:15433\n", 1)
	if string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
}

func TestUsePortsRewritesAJSONConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"listeners":[{"name":"api","protocol":"http","listen":"127.0.0.1:15432","upstream":"127.0.0.1:1"}]}`)
	c := []portConflict{{portUse: portUse{what: "listener api", addr: "127.0.0.1:15432"}, free: "127.0.0.1:15433"}}
	if err := usePorts(path, c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"listen":"127.0.0.1:15433"`) || !strings.Contains(string(b), `"upstream":"127.0.0.1:1"`) {
		t.Errorf("file = %s", b)
	}
}

// The demo API's address is named twice: by the demo key and as the demo
// listener's upstream. Moving it moves both, or the listener would front
// nothing.
func TestUsePortsMovesTheDemoAPIEverywhereItIsNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	writeFile(t, path, configyaml.DemoAPIKey+": 127.0.0.1:18081\nlisteners:\n  - name: demo\n    protocol: http\n"+
		"    listen: 127.0.0.1:18080\n    upstream: 127.0.0.1:18081\n")
	c := []portConflict{{portUse: portUse{what: "demo API", addr: "127.0.0.1:18081", demo: true}, free: "127.0.0.1:18082"}}
	if err := usePorts(path, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "18081") || strings.Count(string(b), "127.0.0.1:18082") != 2 {
		t.Errorf("demo API not moved everywhere:\n%s", b)
	}
}

// Booting a file whose port is taken asks instead of failing, with the free
// port focused; enter takes it, and the boot that follows is on the fixed
// file.
func TestBootAsksForAFreePortWhenOneIsTaken(t *testing.T) {
	dir := t.TempDir()
	busy := hold(t)
	path := filepath.Join(dir, "c.yaml")
	writeFile(t, path, sampleConfig(busy))
	m := homeModel(t, dir, func(string) (string, error) { return "ok", nil })
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "file:"+path {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, cmd := tm.Update(wkey("enter"))
	tm, quit := tm.Update(cmd())
	fm := tm.(firstRunModel)
	if fm.boot != nil || quit != nil || fm.ports == nil {
		t.Fatalf("a config on a taken port was booted (ports %+v)", fm.ports)
	}
	out := ansi.Strip(fm.render())
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{busy, "is in use by another program", "Use the free port and boot", "Go back"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the dialog lacks %q:\n%s", want, out)
		}
	}
	free := fm.ports.conflicts[0].free
	tm, cmd = tm.Update(wkey("enter"))
	if cmd == nil {
		t.Fatalf("enter on the focused answer did not fix and recheck (portErr %q)", tm.(firstRunModel).portErr)
	}
	tm, quit = tm.Update(cmd())
	if b := tm.(firstRunModel).boot; b == nil || quit == nil {
		t.Fatalf("the fixed config did not boot: %+v", b)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "listen: "+free) || !strings.Contains(string(data), "# my notes stay") {
		t.Errorf("the file was not moved to %s, or lost its comments:\n%s", free, data)
	}
}

func TestGoBackFromTheTakenPortDialogDoesNotBoot(t *testing.T) {
	dir := t.TempDir()
	busy := hold(t)
	path := filepath.Join(dir, "c.yaml")
	writeFile(t, path, sampleConfig(busy))
	m := homeModel(t, dir, func(string) (string, error) { return "ok", nil })
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "file:"+path {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, cmd := tm.Update(wkey("enter"))
	tm, _ = tm.Update(cmd())
	tm, _ = tm.Update(wkey("right"))
	tm, cmd = tm.Update(wkey("enter"))
	fm := tm.(firstRunModel)
	if fm.ports != nil || fm.boot != nil || cmd != nil {
		t.Fatal("Go back did not close the dialog without booting")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), busy) {
		t.Error("Go back changed the file")
	}
}

// New configs start on ports nothing holds.
func TestNewDraftsStartOnFreePorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:15432")
	if err != nil {
		t.Skip("15432 is not available to hold in this environment")
	}
	defer ln.Close()
	d, err := newDraft("postgres", false, noMachine())
	if err != nil {
		t.Fatal(err)
	}
	l, _ := d.listener.value()
	if l.Listen == "127.0.0.1:15432" || bindError(l.Listen) != nil {
		t.Errorf("listen = %s, want a free port instead of the held 15432", l.Listen)
	}
}
