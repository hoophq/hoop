package sidecartui

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"

	_ "github.com/hoophq/hoop/sidecar/analyzer/anthropic"
)

// validateFile is the validator the CLI injects, minus the license and the
// reviewer: the free tier, which is what a first config must fit.
func validateFile(path string) (string, error) {
	cfg, err := configyaml.Load(path)
	if err != nil {
		return "", err
	}
	det, err := alcatraz.PluginFromConfig(cfg.PII)
	if err != nil {
		return "", err
	}
	lanes, err := daemon.Validate(cfg, det)
	if err != nil {
		return "", err
	}
	return strings.Repeat("lane ", len(lanes)), nil
}

func validateDraft(t *testing.T, d *draft) error {
	t.Helper()
	cfg, opts, err := d.config()
	if err != nil {
		return err
	}
	b, err := configyaml.Render(cfg, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	_, err = validateBytes(b, t.TempDir(), validateFile)
	return err
}

func noMachine() machine { return machine{keyDir: "/nonexistent/keys"} }

// Every row of the protocol list starts from defaults that boot as they
// are, except SSH: its host key and CA are files on this machine no default
// can name, and the failure must say which field is missing.
func TestEveryChoiceDefaultsToAValidConfig(t *testing.T) {
	choices := append([]string{"demo"}, daemon.Protocols()...)
	for _, p := range choices {
		d, err := newDraft(p, p == "demo", noMachine())
		if err != nil {
			t.Fatalf("newDraft(%s): %v", p, err)
		}
		err = validateDraft(t, d)
		if p == "ssh" {
			if err == nil || !strings.Contains(err.Error(), "no host key") {
				t.Errorf("ssh defaults: err = %v, want one naming the host key", err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s defaults do not validate: %v", p, err)
		}
	}
}

// The analyzer is on by default only when its key file is there, and the
// config it then writes must validate with that key.
func TestAnalyzerDefaultsFollowTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	d, _ := newDraft("postgres", false, machine{keyDir: dir})
	if d.an.on {
		t.Fatal("analyzer on with no key file")
	}
	if err := os.WriteFile(filepath.Join(dir, "anthropic.key"), []byte("sk-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, _ = newDraft("postgres", false, machine{keyDir: dir})
	if !d.an.on {
		t.Fatal("analyzer off although its key file exists")
	}
	if err := validateDraft(t, d); err != nil {
		t.Errorf("analyzer config does not validate: %v", err)
	}
	cfg, _, _ := d.config()
	if cfg.Analyzer == nil || cfg.Listeners[0].Analyzer == nil {
		t.Errorf("analyzer missing from the config: %+v", cfg)
	}
}

// Each rule type's form must produce a rule that validates on the lane it
// is offered for. A form that let a person build a rule the sidecar then
// refuses would fail at boot, after the person was told it was fine.
func TestEveryRuleTypeFormBuildsAValidRule(t *testing.T) {
	fill := map[policy.MatchType]map[string]string{
		policy.MatchDenyWords:    {"words": "drop table"},
		policy.MatchPattern:      {"pattern": `(?i)password`},
		policy.MatchOperation:    {"operations": "delete"},
		policy.MatchTable:        {"tables": "customers"},
		policy.MatchPII:          {"entities": "EMAIL_ADDRESS"},
		policy.MatchHTTPResource: {"resources": "/admin/*", "methods": "DELETE"},
		policy.MatchHTTPStatus:   {"statuses": "5xx"},
		policy.MatchHTTPHeader:   {"headers": "x-env=prod|staging"},
		policy.MatchGRPCStatus:   {"statuses": "UNAVAILABLE"},
	}
	for _, protocol := range []string{"postgres", "http", "grpc"} {
		for _, typ := range ruleTypesFor(protocol, false) {
			d, _ := newDraft(protocol, false, noMachine())
			f := ruleForm(policy.Rule{Type: policy.MatchType(typ)}, protocol, false, true)
			if _, err := ruleFromForm(f); err == nil {
				t.Errorf("%s/%s: an empty rule was accepted", protocol, typ)
			}
			f.byID("name").text = "r"
			for id, v := range fill[policy.MatchType(typ)] {
				if x := f.byID(id); x.kind == fMulti {
					x.multi = strings.Split(v, ",")
				} else {
					x.text = v
				}
			}
			r, err := ruleFromForm(f)
			if err != nil {
				t.Errorf("%s/%s: %v", protocol, typ, err)
				continue
			}
			d.rules = []policy.Rule{r}
			if err := validateDraft(t, d); err != nil {
				t.Errorf("%s/%s rule does not validate: %v", protocol, typ, err)
			}
		}
	}
}

func TestMaskFormRoundTrips(t *testing.T) {
	in := alcatraz.Rule{Name: "cards", Entities: []string{"CREDIT_CARD"}, Strategy: alcatraz.StrategyPartial, KeepLast: 4, MaskChar: '#'}
	out, err := maskFromForm(maskForm(in, false))
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != in.Name || out.Strategy != in.Strategy || out.KeepLast != 4 || out.MaskChar != '#' || out.Entities[0] != "CREDIT_CARD" {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
	f := maskForm(in, false)
	f.byID("keep").text = "four"
	if _, err := maskFromForm(f); err == nil {
		t.Error("a Keep last that is not a number was accepted")
	}
}

func TestListenerFormReadsBackAndRefusesABadNumber(t *testing.T) {
	d, err := newDraft("postgres", false, machine{found: []found{{"postgres", "db.internal:6543", "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := d.listener.value()
	if err != nil {
		t.Fatal(err)
	}
	if l.Upstream != "db.internal:6543" || l.Listen != "127.0.0.1:16543" || l.Name != "postgres" {
		t.Errorf("listener = %+v", l)
	}
	d.listener.advanced.on = true
	d.listener.form.byID("max_conns").text = "lots"
	if _, err := d.listener.value(); err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("err = %v, want one about the number", err)
	}
}

func wkey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// drive runs a command and feeds its message back, the way the program
// loop would, until nothing more comes out.
func drive(t *testing.T, w *wizard, cmd tea.Cmd) wizEvent {
	t.Helper()
	ev := wizNone
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return ev
		}
		cmd, ev = w.update(msg)
	}
	return ev
}

// The whole path a person takes for the demo: pick it, land on the
// overview, which validates, then save and boot. The file must exist and
// validate, and a second save must ask before replacing it.
func TestWizardDemoSaveAndBoot(t *testing.T) {
	dir := t.TempDir()
	w := newWizard(noMachine(), validateFile, dir, time.Now)
	cmd, _ := w.update(wkey("enter")) // Demo is the first row
	if w.page != pgOverview {
		t.Fatalf("page = %v after choosing the demo, want the overview", w.page)
	}
	drive(t, w, cmd)
	if w.verr != nil {
		t.Fatalf("the demo draft does not validate: %v", w.verr)
	}
	for w.overview.selected() != "boot" {
		w.update(wkey("down"))
	}
	cmd, _ = w.update(wkey("enter"))
	if ev := drive(t, w, cmd); ev != wizBoot {
		t.Fatalf("event = %v, saveErr = %v; want wizBoot", ev, w.saveErr)
	}
	path := filepath.Join(dir, configyaml.StarterFile)
	if w.saved != path {
		t.Errorf("saved = %q, want %q", w.saved, path)
	}
	if _, err := validateFile(path); err != nil {
		t.Errorf("the saved demo config does not validate: %v", err)
	}
	data, _ := os.ReadFile(path)
	if v, ok, _ := configyaml.ExtensionValue(data, configyaml.DemoAPIKey); !ok || v == "" {
		t.Errorf("the demo config does not name the demo API:\n%s", data)
	}

	cmd, _ = w.update(wkey("enter"))
	drive(t, w, cmd)
	if !w.existsAsk {
		t.Fatalf("saving over an existing file did not ask (saveErr %v)", w.saveErr)
	}
	cmd, _ = w.update(wkey("y"))
	if ev := drive(t, w, cmd); ev != wizBoot {
		t.Errorf("after y: event = %v, saveErr = %v", ev, w.saveErr)
	}

	// The question is a dialog with Replace focused: enter replaces.
	cmd, _ = w.update(wkey("enter"))
	drive(t, w, cmd)
	out := ansi.Strip(w.overviewView(100, 30))
	if !strings.Contains(out, "Replace "+configyaml.StarterFile+"?") || !strings.Contains(out, "Yes, replace it") {
		t.Fatalf("the replace dialog is not drawn:\n%s", out)
	}
	cmd, _ = w.update(wkey("enter"))
	if ev := drive(t, w, cmd); ev != wizBoot {
		t.Errorf("enter on the focused Replace: event = %v, saveErr = %v", ev, w.saveErr)
	}

	// No keeps the file and puts the cursor on File, to rename it.
	cmd, _ = w.update(wkey("enter"))
	drive(t, w, cmd)
	w.update(wkey("right"))
	if cmd, _ = w.update(wkey("enter")); cmd != nil || w.existsAsk {
		t.Fatalf("No did not close the dialog without saving")
	}
	if w.overview.selected() != "file" {
		t.Errorf("cursor on %q after No, want file", w.overview.selected())
	}
}

// The overview leads with its actions: Save and boot is where the cursor
// starts, Back sits under Save only, and the note between them and the
// fields is text the cursor never lands on.
func TestOverviewLeadsWithItsActions(t *testing.T) {
	w := newWizard(noMachine(), validateFile, t.TempDir(), time.Now)
	w.update(wkey("enter")) // the demo
	var ids []string
	for _, it := range w.overview.items {
		ids = append(ids, it.id)
	}
	if got := strings.Join(ids, ","); got != "boot,save,back,,listener,rules,masks,analyzer,pii,file" {
		t.Fatalf("overview order = %s", got)
	}
	if w.overview.selected() != "boot" {
		t.Errorf("cursor starts on %q, want boot", w.overview.selected())
	}
	w.update(wkey("down"))
	w.update(wkey("down"))
	w.update(wkey("down"))
	if w.overview.selected() != "listener" {
		t.Errorf("three downs from boot landed on %q, want listener past the note", w.overview.selected())
	}
	w.update(wkey("up"))
	if w.overview.selected() != "back" {
		t.Errorf("up from listener landed on %q, want back", w.overview.selected())
	}
	if out := ansi.Strip(w.overview.view(80, 20)); !strings.Contains(out, "◀ Back") || strings.Contains(out, "▶ Back") {
		t.Errorf("a focused Back must point back:\n%s", out)
	}
	w.update(wkey("enter"))
	if w.page != pgProtocol {
		t.Errorf("Back went to page %v, want the protocol list", w.page)
	}
	out := ansi.Strip(func() string { w.update(wkey("enter")); return w.overviewView(100, 30) }())
	if i, j := strings.Index(out, "Save only"), strings.Index(out, "Listener"); i < 0 || j < 0 || i > j {
		t.Errorf("Save only is not drawn above the fields:\n%s", out)
	}
	if !strings.Contains(out, "To change one, move to it and press enter") {
		t.Errorf("the note is not drawn:\n%s", out)
	}
}

// The listener page leads with Continue and Back like the overview, and
// the note under them is skipped by the cursor.
func TestListenerPageLeadsWithItsActions(t *testing.T) {
	w := newWizard(machine{found: []found{{"postgres", "127.0.0.1:5432", "test"}}}, validateFile, t.TempDir(), time.Now)
	for w.protos.selected() != "postgres" {
		w.update(wkey("down"))
	}
	w.update(wkey("enter"))
	f := w.d.listener.form
	if f.focused().id != "done" || f.fields[1].id != "back" || f.fields[2].kind != fNote {
		t.Fatalf("listener page starts with %q, %q; want Continue, Back, then the note", f.fields[0].id, f.fields[1].id)
	}
	w.update(wkey("down"))
	w.update(wkey("down"))
	if f.focused().id != "name" {
		t.Errorf("two downs from Continue landed on %q, want name past the note", f.focused().id)
	}
	out := ansi.Strip(f.view(90, 24))
	if i, j := strings.Index(out, "Back"), strings.Index(out, "Upstream"); i < 0 || j < 0 || i > j {
		t.Errorf("Back is not drawn above the fields:\n%s", out)
	}
	w.update(wkey("up"))
	w.update(wkey("enter"))
	if w.page != pgProtocol {
		t.Errorf("Back went to page %v, want the protocol list", w.page)
	}
	w.update(wkey("enter"))
	if f.focused().id != "back" {
		t.Errorf("returning keeps the cursor where it was, got %q", f.focused().id)
	}
	f.cur = 0
	w.update(wkey("enter"))
	if w.page != pgOverview {
		t.Errorf("Continue went to page %v, want the overview", w.page)
	}
}

// A draft that does not validate is never written: the person would boot
// a file the sidecar refuses.
func TestWizardDoesNotSaveAnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	w := newWizard(noMachine(), func(string) (string, error) { return "", errors.New("nope") }, dir, time.Now)
	w.update(wkey("enter"))
	cmd := w.save(true, false)
	if ev := drive(t, w, cmd); ev != wizNone || w.saveErr == nil {
		t.Fatalf("event = %v, saveErr = %v; want a refusal", ev, w.saveErr)
	}
	if _, err := os.Stat(filepath.Join(dir, configyaml.StarterFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an invalid config was written: %v", err)
	}
}

func TestFirstRunModelBootsWhatTheWizardSaved(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	m := newFirstRunModel("v1", func() time.Time { return now }, func() {}, noMachine, validateFile, dir)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm, cmd := tm.Update(wkey("w"))
	tm, _ = tm.Update(cmd())
	if tm.(firstRunModel).wiz == nil {
		t.Fatal("w did not open the setup screens")
	}
	tm, cmd = tm.Update(wkey("enter"))
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(wizValidatedMsg); !ok {
			break
		}
		tm, cmd = tm.Update(msg)
	}
	fm := tm.(firstRunModel)
	cmd = fm.wiz.save(true, false)
	// Saved; the boot is checked like any other, ports included.
	tm, cmd = tm.Update(cmd())
	if cmd == nil {
		t.Fatal("Save and boot did not check the saved config before booting")
	}
	tm, quit := tm.Update(cmd())
	if b := tm.(firstRunModel).boot; b == nil || b.ConfigPath != filepath.Join(dir, configyaml.StarterFile) {
		t.Fatalf("boot = %+v", b)
	}
	if quit == nil {
		t.Fatal("booting did not quit the first-run screen")
	}
}

// Every screen draws at every size without a line wider than the terminal
// or a frame taller than it.
func TestScreensRenderAtAnySize(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sizes := [][2]int{{20, 5}, {60, 14}, {72, 24}, {100, 30}, {200, 60}}
	check := func(name string, out string, sz [2]int) {
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%s %dx%d: %d lines", name, sz[0], sz[1], len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > sz[0] {
				t.Errorf("%s %dx%d: a line is %d wide", name, sz[0], sz[1], w)
				return
			}
		}
	}
	for _, sz := range sizes {
		m := newFirstRunModel("v1", func() time.Time { return now }, func() {}, noMachine, validateFile, t.TempDir())
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		tm, _ = tm.Update(frReadyMsg{url: "http://127.0.0.1:15321"})
		tm, _ = tm.Update(frVisitMsg(now.Add(-300 * time.Millisecond)))
		check("home", tm.(firstRunModel).render(), sz)

		fm := tm.(firstRunModel)
		fm.wiz = newWizard(noMachine(), validateFile, t.TempDir(), func() time.Time { return now })
		check("protocols", fm.render(), sz)
		fm.wiz.update(wkey("down")) // clickhouse
		fm.wiz.update(wkey("enter"))
		check("listener", fm.render(), sz)
		fm.wiz.toOverview()
		check("overview", fm.render(), sz)
		fm.wiz.page, fm.wiz.form = pgRule, ruleForm(policy.Rule{}, "clickhouse", false, true)
		check("rule", fm.render(), sz)
		fm.wiz.form.byID("type").text = string(policy.MatchPII)
		fm.wiz.form.cur = 2
		fm.wiz.form.update(wkey("down"))
		fm.wiz.form.pick = newChecklist("Data types", alcatraz.AllEntities(), nil, nil)
		check("checklist", fm.render(), sz)
	}
}

// ---- detection --------------------------------------------------------------

func withProbes(t *testing.T, p []struct {
	protocol string
	port     int
}) {
	t.Helper()
	orig := probes
	probes = p
	t.Cleanup(func() { probes = orig })
}

func fakeEnv(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// A backend found on another machine is reached with TLS by default, and the
// config that writes still validates for every protocol a URL can name. One
// found on this machine, or a default, stays plain.
func TestARemoteFoundUpstreamDefaultsToTLS(t *testing.T) {
	for _, p := range []string{"postgres", "mysql", "mssql", "mongodb", "clickhouse"} {
		remote := machine{found: []found{{p, "db.example.com:" + map[string]string{
			"postgres": "5432", "mysql": "3306", "mssql": "1433", "mongodb": "27017", "clickhouse": "8123"}[p], "DATABASE_URL"}}}
		d, err := newDraft(p, false, remote)
		if err != nil {
			t.Fatal(err)
		}
		l, err := d.listener.value()
		if err != nil {
			t.Fatal(err)
		}
		if l.UpstreamTLS == nil {
			t.Errorf("%s: a remote upstream defaults to plain", p)
		}
		if err := validateDraft(t, d); err != nil {
			t.Errorf("%s: the TLS default does not validate: %v", p, err)
		}
		if !strings.Contains(ansi.Strip(d.listener.form.view(100, 30)), "Upstream TLS is on") {
			t.Errorf("%s: the listener page does not say why TLS is on", p)
		}
	}
	local := machine{found: []found{{"postgres", "127.0.0.1:5432", "port 5432 is open"}}}
	d, _ := newDraft("postgres", false, local)
	if l, _ := d.listener.value(); l.UpstreamTLS != nil {
		t.Error("a backend on this machine defaults to TLS")
	}
}

func TestFromDatabaseURL(t *testing.T) {
	for _, c := range []struct {
		in       string
		ok       bool
		protocol string
		addr     string
	}{
		{"postgres://u:p@db.internal/app", true, "postgres", "db.internal:5432"},
		{"postgresql://db:6543/app", true, "postgres", "db:6543"},
		{"mysql://root@127.0.0.1/app", true, "mysql", "127.0.0.1:3306"},
		{"sqlserver://sa@mssql:14330?database=x", true, "mssql", "mssql:14330"},
		{"mssql://h/db", true, "mssql", "h:1433"},
		{"mongodb://m1/db", true, "mongodb", "m1:27017"},
		{"clickhouse://ch/db", true, "clickhouse", "ch:8123"},
		{"postgres://[::1]:5433/app", true, "postgres", "[::1]:5433"},
		{"", false, "", ""},
		{"redis://cache:6379", false, "", ""},        // a scheme the sidecar does not front
		{"postgres:///app?host=/tmp", false, "", ""}, // no host: a socket path
		{"not a url at all", false, "", ""},          // no scheme, no host
		{"postgres://%zz@db/app", false, "", ""},     // malformed escape
	} {
		f, ok := fromDatabaseURL(c.in)
		if ok != c.ok || (ok && (f.protocol != c.protocol || f.addr != c.addr || f.source != "DATABASE_URL")) {
			t.Errorf("fromDatabaseURL(%q) = %+v, %v; want %s %s, %v", c.in, f, ok, c.protocol, c.addr, c.ok)
		}
	}
}

func TestDetectPrefersDatabaseURLOverAnOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	withProbes(t, []struct {
		protocol string
		port     int
	}{{"mysql", ln.Addr().(*net.TCPAddr).Port}})

	m := detect(context.Background(), fakeEnv(map[string]string{
		"DATABASE_URL":      "postgres://app:secret@db.internal/app",
		"ANTHROPIC_API_KEY": "sk-test",
	}))
	if len(m.found) != 2 || m.found[0] != (found{"postgres", "db.internal:5432", "DATABASE_URL"}) || m.found[1].protocol != "mysql" {
		t.Errorf("found = %+v", m.found)
	}
	if m.provider != "anthropic" {
		t.Errorf("provider = %q", m.provider)
	}
	if _, ok := m.foundFor("mysql"); !ok {
		t.Error("foundFor(mysql) missed the open port")
	}
}
