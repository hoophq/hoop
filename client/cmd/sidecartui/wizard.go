package sidecartui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The setup screens: pick what to protect, say where it is, review the
// defaults (each one opens an editor), then save, or save and boot.

type wizPage int

const (
	pgProtocol wizPage = iota
	pgListener
	pgOverview
	pgRules
	pgRule
	pgMasks
	pgMask
	pgAnalyzer
	pgPII
	pgFile
)

var pageTitle = map[wizPage]string{
	pgProtocol: "What should the sidecar protect?", pgListener: "Where is it?",
	pgOverview: "Your config", pgRules: "Guardrails", pgRule: "Guardrail rule", pgMasks: "Data masking",
	pgMask: "Masking rule", pgAnalyzer: "AI analyzer", pgPII: "Sensitive data detection", pgFile: "File",
}

// Boot is what the setup screens hand back when the person chose to start
// the sidecar on what they wrote.
type Boot struct {
	// ConfigPath is the saved config.
	ConfigPath string
}

type (
	wizValidatedMsg struct {
		seq     int
		summary string
		err     error
	}
	wizSavedMsg struct {
		path    string
		summary string
		err     error
		exists  bool
		boot    bool
	}
)

// errDraft marks a config the draft itself could not build: a listener
// field that does not parse, before any validator ran.
var errDraft = errors.New("the config is incomplete")

type wizard struct {
	now  func() time.Time
	page wizPage
	mach machine
	d    *draft

	protos   menu
	overview menu
	list     menu
	form     *form
	editing  int

	validate func(path string) (string, error)
	dir      string

	vseq     int
	vrunning bool
	vsummary string
	verr     error

	saving    bool
	saveErr   error
	existsAsk bool
	saved     string
}

func newWizard(mach machine, validate func(string) (string, error), dir string, now func() time.Time) *wizard {
	w := &wizard{now: now, page: pgProtocol, mach: mach, validate: validate, dir: dir}
	w.protos.items = append(w.protos.items, menuItem{id: "demo", label: "Demo",
		detail: "an invented API with fake users, nothing to set up"})
	for _, p := range daemon.Protocols() {
		detail := daemon.ProtocolLabel(p)
		if f, ok := mach.foundFor(p); ok {
			detail += "  · found " + f.addr + " (" + f.source + ")"
		}
		w.protos.items = append(w.protos.items, menuItem{id: p, label: p, detail: detail})
	}
	return w
}

// wizEvent tells the first-run screen what the setup screens decided.
type wizEvent int

const (
	wizNone wizEvent = iota
	wizExit
	wizBoot
	wizSavedOnly
)

func (w *wizard) update(msg tea.Msg) (tea.Cmd, wizEvent) {
	switch msg := msg.(type) {
	case wizValidatedMsg:
		if msg.seq == w.vseq {
			w.vrunning, w.vsummary, w.verr = false, msg.summary, msg.err
		}
		return nil, wizNone
	case wizSavedMsg:
		w.saving = false
		if msg.err != nil {
			w.saveErr, w.existsAsk = msg.err, msg.exists
			return nil, wizNone
		}
		w.saved, w.saveErr, w.vsummary, w.verr = msg.path, nil, msg.summary, nil
		if msg.boot {
			return nil, wizBoot
		}
		return nil, wizSavedOnly
	case tea.KeyPressMsg:
		return w.key(msg)
	}
	return nil, wizNone
}

func (w *wizard) key(k tea.KeyPressMsg) (tea.Cmd, wizEvent) {
	switch w.page {
	case pgProtocol:
		switch id := w.protos.update(k); {
		case id == "back":
			return nil, wizExit
		case id == "" || strings.HasPrefix(id, "key:"):
		default:
			return w.choose(id)
		}
	case pgListener:
		cmd, ev := w.d.listener.form.update(k)
		switch ev {
		case "back":
			w.page = pgProtocol
		case "done":
			if _, err := w.d.listener.value(); err != nil {
				w.d.listener.form.err = err.Error()
				return cmd, wizNone
			}
			return w.toOverview()
		}
		return cmd, wizNone
	case pgOverview:
		return w.overviewKey(k)
	case pgRules, pgMasks:
		return w.listKey(k)
	case pgRule, pgMask, pgAnalyzer, pgPII, pgFile:
		return w.formKey(k)
	}
	return nil, wizNone
}

// choose starts a draft for protocol, or keeps the current one when the
// person came back and picked the same thing.
func (w *wizard) choose(id string) (tea.Cmd, wizEvent) {
	demo := id == "demo"
	if w.d == nil || w.d.demo != demo || (!demo && w.d.protocol != id) {
		d, err := newDraft(id, demo, w.mach)
		if err != nil {
			w.saveErr = err
			return nil, wizNone
		}
		w.d = d
	}
	if demo {
		return w.toOverview()
	}
	w.page = pgListener
	return nil, wizNone
}

func (w *wizard) toOverview() (tea.Cmd, wizEvent) {
	w.page = pgOverview
	w.form = nil
	w.buildOverview()
	return w.startValidate(), wizNone
}

func (w *wizard) buildOverview() {
	l, err := w.d.listenerValue()
	ldetail := fmt.Sprintf("%s  %s → %s", l.Protocol, l.Listen, orDash(l.Upstream))
	if err != nil {
		ldetail = "✕ " + err.Error()
	}
	if w.d.demo {
		ldetail += "  (the demo API)"
	}
	cur := w.overview.cur
	w.overview.items = []menuItem{
		{id: "listener", label: "Listener", detail: ldetail},
		{id: "rules", label: "Guardrails", detail: w.d.rulesSummary()},
		{id: "masks", label: "Data masking", detail: w.d.masksSummary()},
		{id: "analyzer", label: "AI analyzer", detail: w.d.analyzerSummary()},
		{id: "pii", label: "Sensitive data", detail: w.d.piiSummary()},
		{id: "file", label: "File", detail: w.d.file},
		{id: "boot", label: "Save and boot", button: true},
		{id: "save", label: "Save only", button: true},
	}
	w.overview.cur = min(cur, len(w.overview.items)-1)
}

func (w *wizard) overviewKey(k tea.KeyPressMsg) (tea.Cmd, wizEvent) {
	if w.existsAsk {
		switch k.String() {
		case "y", "o":
			w.existsAsk = false
			return w.save(w.pendingBoot(), true), wizNone
		case "n", "esc":
			w.existsAsk, w.saveErr = false, nil
		}
		return nil, wizNone
	}
	if w.saving {
		return nil, wizNone
	}
	switch id := w.overview.update(k); id {
	case "back":
		if w.d.demo {
			w.page = pgProtocol
		} else {
			w.page = pgListener
		}
	case "listener":
		if w.d.demo {
			w.saveErr = errors.New("the demo's listener is fixed: pick a protocol to set your own")
			return nil, wizNone
		}
		w.page = pgListener
	case "rules":
		w.page, w.list.cur = pgRules, 0
		w.buildList()
	case "masks":
		w.page, w.list.cur = pgMasks, 0
		w.buildList()
	case "analyzer":
		w.page, w.form = pgAnalyzer, analyzerForm(w.d.an, w.d.protocol)
	case "pii":
		w.page, w.form = pgPII, piiForm(w.d.pii)
	case "file":
		w.page, w.form = pgFile, newForm("File",
			&field{id: "file", label: "Save as", kind: fText, text: w.d.file, placeholder: configyaml.StarterFile,
				help: "A .yaml or .json path, relative to the current directory."},
			&field{id: "done", label: "Done", kind: fButton})
	case "boot":
		return w.save(true, false), wizNone
	case "save":
		return w.save(false, false), wizNone
	}
	return nil, wizNone
}

func (w *wizard) pendingBoot() bool { return w.overview.selected() == "boot" }

func (w *wizard) buildList() {
	var items []menuItem
	if w.page == pgRules {
		for i, r := range w.d.rules {
			items = append(items, menuItem{id: fmt.Sprint(i), label: r.Name, detail: ruleTypeLabel[string(r.Type)] + "  " + ruleDetail(r)})
		}
		mode := "enforce: matches are refused"
		if w.d.guardMode == "observe" {
			mode = "observe: matches are only recorded"
		}
		items = append(items, menuItem{id: "mode", label: "Mode", detail: mode})
		items = append(items, menuItem{id: "add", label: "+ Add a rule", button: true}, menuItem{id: "done", label: "Done", button: true})
	} else {
		for i, r := range w.d.masks {
			items = append(items, menuItem{id: fmt.Sprint(i), label: r.Name, detail: maskDetail(r)})
		}
		items = append(items, menuItem{id: "add", label: "+ Add a rule", button: true}, menuItem{id: "done", label: "Done", button: true})
	}
	w.list.items = items
	w.list.cur = min(w.list.cur, len(items)-1)
}

func (w *wizard) listKey(k tea.KeyPressMsg) (tea.Cmd, wizEvent) {
	id := w.list.update(k)
	switch {
	case id == "back" || id == "done":
		return w.toOverview()
	case id == "mode":
		if w.d.guardMode == "observe" {
			w.d.guardMode = ""
		} else {
			w.d.guardMode = "observe"
		}
		w.buildList()
	case id == "add":
		w.editing = -1
		if w.page == pgRules {
			w.page, w.form = pgRule, ruleForm(newRule(w.d, len(w.d.rules)), w.d.protocol, w.d.captures(), true)
		} else {
			w.page, w.form = pgMask, maskForm(newMask(len(w.d.masks)), true)
		}
	case id == "key:d" || id == "key:x" || id == "key:delete" || id == "key:backspace":
		var i int
		if _, err := fmt.Sscan(w.list.selected(), &i); err == nil {
			if w.page == pgRules && i < len(w.d.rules) {
				w.d.rules = append(w.d.rules[:i], w.d.rules[i+1:]...)
			} else if w.page == pgMasks && i < len(w.d.masks) {
				w.d.masks = append(w.d.masks[:i], w.d.masks[i+1:]...)
			}
			w.buildList()
		}
	case id != "" && !strings.HasPrefix(id, "key:"):
		var i int
		if _, err := fmt.Sscan(id, &i); err != nil {
			return nil, wizNone
		}
		w.editing = i
		if w.page == pgRules {
			w.page, w.form = pgRule, ruleForm(w.d.rules[i], w.d.protocol, w.d.captures(), false)
		} else {
			w.page, w.form = pgMask, maskForm(w.d.masks[i], false)
		}
	}
	return nil, wizNone
}

func newRule(d *draft, n int) policy.Rule {
	types := ruleTypesFor(d.protocol, d.captures())
	return policy.Rule{Name: fmt.Sprintf("rule-%d", n+1), Type: policy.MatchType(types[0])}
}

func (w *wizard) formKey(k tea.KeyPressMsg) (tea.Cmd, wizEvent) {
	cmd, ev := w.form.update(k)
	parent := map[wizPage]wizPage{pgRule: pgRules, pgMask: pgMasks}
	switch ev {
	case "back":
		if p, ok := parent[w.page]; ok {
			w.page = p
			w.buildList()
			return cmd, wizNone
		}
		return w.toOverview()
	case "delete":
		if w.page == pgRule && w.editing >= 0 {
			w.d.rules = append(w.d.rules[:w.editing], w.d.rules[w.editing+1:]...)
		}
		if w.page == pgMask && w.editing >= 0 {
			w.d.masks = append(w.d.masks[:w.editing], w.d.masks[w.editing+1:]...)
		}
		w.page = parent[w.page]
		w.buildList()
	case "done":
		var err error
		switch w.page {
		case pgRule:
			var r policy.Rule
			if r, err = ruleFromForm(w.form); err == nil {
				if w.editing < 0 {
					w.d.rules = append(w.d.rules, r)
				} else {
					w.d.rules[w.editing] = r
				}
			}
		case pgMask:
			var r alcatraz.Rule
			if r, err = maskFromForm(w.form); err == nil {
				if w.editing < 0 {
					w.d.masks = append(w.d.masks, r)
				} else {
					w.d.masks[w.editing] = r
				}
			}
		case pgAnalyzer:
			w.d.an, err = analyzerFromForm(w.form, w.d.an)
		case pgPII:
			w.d.pii, err = piiFromForm(w.form)
		case pgFile:
			name := strings.TrimSpace(w.form.byID("file").text)
			if name == "" {
				err = errors.New("give the file a name")
			} else if !configyaml.IsYAML(name) && !strings.HasSuffix(strings.ToLower(name), ".json") {
				err = errors.New("use a .yaml, .yml or .json name: the extension picks the parser")
			} else if strings.HasSuffix(strings.ToLower(name), ".json") {
				err = errors.New("the setup screen writes YAML: use a .yaml or .yml name")
			}
			if err == nil {
				w.d.file = name
			}
		}
		if err != nil {
			w.form.err = err.Error()
			return cmd, wizNone
		}
		if p, ok := parent[w.page]; ok {
			w.page = p
			w.buildList()
			return cmd, wizNone
		}
		return w.toOverview()
	}
	return cmd, wizNone
}

// render turns the draft into the file's bytes.
func (w *wizard) render() ([]byte, error) {
	cfg, opts, err := w.d.config()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDraft, err)
	}
	return configyaml.Render(cfg, opts)
}

// startValidate checks the draft the way --validate would, off the UI
// goroutine. A newer run makes an older answer stale.
func (w *wizard) startValidate() tea.Cmd {
	w.vseq++
	seq := w.vseq
	w.vrunning, w.verr, w.vsummary = true, nil, ""
	b, err := w.render()
	validate, dir := w.validate, w.dir
	return func() tea.Msg {
		if err != nil {
			return wizValidatedMsg{seq: seq, err: err}
		}
		summary, verr := validateBytes(b, dir, validate)
		return wizValidatedMsg{seq: seq, summary: summary, err: verr}
	}
}

// validateBytes writes b to a temporary file beside where it will be
// saved, so relative paths in it resolve the same, and validates that.
func validateBytes(b []byte, dir string, validate func(string) (string, error)) (string, error) {
	if validate == nil {
		return "", errors.New("this build has no validator")
	}
	f, err := os.CreateTemp(dir, ".hoop-sidecar-check-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	return validate(f.Name())
}

// save validates the draft, and only then writes it: a file that does not
// validate is never left behind as if it were a config. An existing file is
// replaced only after the person said yes.
func (w *wizard) save(boot, overwrite bool) tea.Cmd {
	w.saving, w.saveErr = true, nil
	b, err := w.render()
	path := w.d.file
	if w.dir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(w.dir, path)
	}
	validate, dir := w.validate, w.dir
	return func() tea.Msg {
		if err != nil {
			return wizSavedMsg{err: err, boot: boot}
		}
		summary, verr := validateBytes(b, dir, validate)
		if verr != nil {
			return wizSavedMsg{err: fmt.Errorf("not saved, the config does not validate: %w", verr), boot: boot}
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if overwrite {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return wizSavedMsg{err: fmt.Errorf("%s already exists", path), exists: true, boot: boot}
			}
			return wizSavedMsg{err: err, boot: boot}
		}
		_, werr := f.Write(b)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return wizSavedMsg{err: fmt.Errorf("writing %s: %w", path, werr), boot: boot}
		}
		return wizSavedMsg{path: path, summary: summary, boot: boot}
	}
}

// ---- view -------------------------------------------------------------------

func (w *wizard) crumbs() string {
	parts := []string{"Set up"}
	switch w.page {
	case pgProtocol:
	case pgListener:
		parts = append(parts, w.d.protocol)
	default:
		name := w.d.protocol
		if w.d.demo {
			name = "demo"
		}
		parts = append(parts, name)
		if w.page != pgOverview {
			parts = append(parts, pageTitle[w.page])
		}
	}
	return strings.Join(parts, " › ")
}

func (w *wizard) intro() string {
	switch w.page {
	case pgProtocol:
		return "Pick the demo to see it work with nothing set up, or the protocol of the backend you want to protect."
	case pgListener:
		return "Where the backend is, and where clients reach the sidecar instead. Defaults come from this machine."
	case pgOverview:
		return "These are on by default. Enter on any of them to change it, then save and boot."
	case pgRules:
		return "Rules decide what may run. Enter edits, d deletes. The free tier enforces one rule; a license lifts it."
	case pgMasks:
		return "Rules rewrite sensitive values in results. Enter edits, d deletes. The free tier applies one rule."
	case pgAnalyzer:
		return "A model rates each statement's risk; the risk decides what happens. It catches what no rule can express."
	case pgPII:
		return "What the detector looks for, for masking and for sensitive-data guardrails."
	}
	return ""
}

func (w *wizard) view(width, height int) string {
	var body string
	switch w.page {
	case pgProtocol:
		body = w.protos.view(width, height)
	case pgListener:
		body = w.d.listener.form.view(width, height)
	case pgOverview:
		body = w.overviewView(width, height)
	case pgRules, pgMasks:
		body = w.list.view(width, height)
	default:
		body = w.form.view(width, height)
	}
	return body
}

func (w *wizard) overviewView(width, height int) string {
	var status []string
	switch {
	case w.saving:
		status = append(status, shimmer("validating and saving…", w.now()))
	case w.existsAsk:
		status = append(status, stDanger.Bold(true).Render("✕ "+w.saveErr.Error()),
			stText.Render("Replace it? ")+stKey.Render("y")+stFaint.Render(" replace  ")+stKey.Render("n")+stFaint.Render(" keep it, then rename under File"))
	case w.saveErr != nil:
		status = append(status, wrapLines(stDanger.Bold(true), "✕ "+w.saveErr.Error(), width)...)
	case w.vrunning:
		status = append(status, shimmer("checking the config like --validate…", w.now()))
	case w.verr != nil:
		status = append(status, wrapLines(stDanger, "✕ "+w.verr.Error(), width)...)
	case w.vsummary != "":
		status = append(status, stPrimary.Render("✓ valid")+stFaint.Render("  "+w.vsummary))
	}
	if w.d.demo {
		status = append(status, "", stFaint.Render("Once it boots, try it from another terminal:"))
		for _, c := range sidecardemo.TryCommands {
			status = append(status, stText.Render("  "+ansi.Truncate(c, width-2, "…")))
		}
	}
	room := max(height-len(status)-1, 4)
	return w.overview.view(width, room) + "\n\n" + strings.Join(status, "\n")
}

func wrapLines(st lipgloss.Style, s string, width int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for ansi.StringWidth(l) > width && width > 10 {
			out = append(out, st.Render(l[:width]))
			l = "  " + l[width:]
		}
		out = append(out, st.Render(l))
	}
	if len(out) > 6 {
		out = append(out[:6], stFaint.Render("  …"))
	}
	return out
}

func (w *wizard) hints() string {
	k := func(key, what string) string { return stKey.Render(key) + stFaint.Render(" "+what+"   ") }
	switch w.page {
	case pgProtocol:
		return k("↑↓", "move") + k("enter", "choose") + k("esc", "back")
	case pgOverview:
		return k("↑↓", "move") + k("enter", "open") + k("esc", "back")
	case pgRules, pgMasks:
		return k("↑↓", "move") + k("enter", "edit") + k("d", "delete") + k("esc", "done")
	}
	if w.form != nil && w.form.pick != nil {
		return ""
	}
	return k("↑↓", "fields") + k("‹ ›", "change") + k("space", "toggle") + k("esc", "back")
}
