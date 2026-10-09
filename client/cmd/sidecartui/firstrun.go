package sidecartui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// FirstRunOptions is what the first-run screen needs from the CLI.
type FirstRunOptions struct {
	Version string
	// Validate checks a config file the way the boot that follows will
	// load it, and returns a one-line summary. The CLI injects it: it
	// links the YAML loader, the PII plugin and the license resolution.
	Validate func(path string) (string, error)
	// Getenv reads the environment for detection; os.Getenv when nil.
	Getenv func(string) string
	// Dir is where the config is saved; the current directory when "".
	Dir string
}

// Messages first-run mode sends the screen.
type (
	frReadyMsg struct {
		url      string
		fellBack bool
	}
	frVisitMsg    time.Time
	frFrameMsg    time.Time
	frDetectedMsg machine
	// frCheckedMsg is a chosen config's validation: nil err boots it.
	frCheckedMsg struct {
		path string
		err  error
	}
)

// frFrame is the animation step: fast enough that the wordmark's band and a
// visit's pulse move smoothly, and it costs one small redraw.
const frFrame = 70 * time.Millisecond

// pulseLife is how long one visit's pulse takes to cross the flow diagram.
const pulseLife = 1400 * time.Millisecond

type firstRunModel struct {
	now     func() time.Time
	version string
	started time.Time

	width, height int

	url      string
	fellBack bool
	ready    bool

	visits    int
	lastVisit time.Time
	// pulses are the start times of visits whose pulse is still crossing.
	pulses []time.Time

	// detect learns the machine for the setup screens; validate and dir
	// are handed to them. detecting is drawn while detect runs.
	detect    func() machine
	validate  func(string) (string, error)
	dir       string
	detecting bool
	wiz       *wizard
	// saved is the config a "save only" wrote, shown on the home screen.
	saved string
	// boot is set when the person chose to start the sidecar on a saved
	// config; the screen quits and the caller boots it.
	boot *Boot

	// home is the Get started list: set up, open a file, then the
	// configs found in dir. picker is the file navigator while it is open.
	home   menu
	picker *filePicker
	// checking is the file being validated before a boot; invalid is the
	// file that failed, shown until the next choice.
	checking string
	invalid  string

	stop     func()
	stopping bool
	done     bool
	err      error
}

func newFirstRunModel(version string, now func() time.Time, stop func(), detect func() machine,
	validate func(string) (string, error), dir string) firstRunModel {
	if version == "" || version == "unknown" {
		version = "dev"
	}
	m := firstRunModel{now: now, version: version, started: now(), stop: stop,
		detect: detect, validate: validate, dir: dir}
	m.buildHome()
	return m
}

// buildHome lists what the person can do, in the order they most likely
// want it: write a config, point at one they have, or pick one of the
// configs already in this folder. The folder is read again each time, so a
// config the setup screens just saved appears in it.
func (m *firstRunModel) buildHome() {
	items := []menuItem{
		{id: "setup", label: "Set up a config file", detail: "pick the demo or a protocol, adjust the defaults, boot"},
		{id: "open", label: "Open a config file", detail: "browse to a config you already have and boot it"},
		{note: true, label: "Config files in this folder"},
	}
	files := configsIn(m.dir)
	for _, f := range files {
		label := filepath.Base(f)
		if f == m.invalid {
			items = append(items, menuItem{id: "file:" + f, label: label, detail: "not a valid sidecar config"})
			continue
		}
		items = append(items, menuItem{id: "file:" + f, label: label, detail: "boot the sidecar with it"})
	}
	if len(files) == 0 {
		items = append(items, menuItem{note: true, label: "No config in this directory"})
	}
	cur := m.home.cur
	m.home.items = items
	m.home.cur = min(cur, len(items)-1)
	if m.home.items[m.home.cur].note {
		m.home.cur = 0
	}
}

// check validates path off the UI goroutine before it is booted.
func (m *firstRunModel) check(path string) tea.Cmd {
	m.checking, m.invalid = path, ""
	validate := m.validate
	return func() tea.Msg {
		if validate == nil {
			return frCheckedMsg{path: path, err: errors.New("this build has no validator")}
		}
		_, err := validate(path)
		return frCheckedMsg{path: path, err: err}
	}
}

// startSetup learns the machine, then opens the setup screens.
func (m *firstRunModel) startSetup() tea.Cmd {
	if m.detecting || m.detect == nil {
		return nil
	}
	m.detecting, m.invalid = true, ""
	detect := m.detect
	return func() tea.Msg { return frDetectedMsg(detect()) }
}

func frTick() tea.Cmd {
	return tea.Tick(frFrame, func(t time.Time) tea.Msg { return frFrameMsg(t) })
}

func (m firstRunModel) Init() tea.Cmd { return frTick() }

func (m firstRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case frReadyMsg:
		m.url, m.fellBack, m.ready = msg.url, msg.fellBack, true
		return m, nil
	case frVisitMsg:
		m.visits++
		m.lastVisit = time.Time(msg)
		m.pulses = append(m.pulses, time.Time(msg))
		return m, nil
	case frFrameMsg:
		now := time.Time(msg)
		live := m.pulses[:0]
		for _, p := range m.pulses {
			if now.Sub(p) < pulseLife {
				live = append(live, p)
			}
		}
		m.pulses = live
		return m, frTick()
	case frDetectedMsg:
		m.detecting = false
		m.wiz = newWizard(machine(msg), m.validate, m.dir, m.now)
		return m, nil
	case doneMsg:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case frCheckedMsg:
		if msg.path != m.checking {
			return m, nil
		}
		m.checking = ""
		if msg.err != nil {
			// Not the validator's text: the person chose a file to run,
			// and the answer is that it cannot be. The reason is one
			// command away, named on the screen.
			m.invalid = msg.path
			m.buildHome()
			return m, nil
		}
		m.picker = nil
		m.boot = &Boot{ConfigPath: msg.path}
		return m, tea.Quit
	}
	if m.wiz != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "ctrl+c" {
			return m.quit()
		}
		cmd, ev := m.wiz.update(msg)
		switch ev {
		case wizExit:
			m.wiz = nil
			m.buildHome()
		case wizSavedOnly:
			m.saved, m.wiz = m.wiz.saved, nil
			m.buildHome()
		case wizBoot:
			m.boot = &Boot{ConfigPath: m.wiz.saved}
			return m, tea.Quit
		}
		return m, cmd
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if k.String() == "ctrl+c" {
		return m.quit()
	}
	if m.checking != "" {
		return m, nil
	}
	if m.picker != nil {
		switch path := m.picker.update(k); path {
		case "":
		case "back":
			m.picker, m.invalid = nil, ""
		default:
			return m, m.check(path)
		}
		return m, nil
	}
	switch k.String() {
	case "q", "esc":
		return m.quit()
	case "w", "s":
		return m, m.startSetup()
	case "o":
		m.picker, m.invalid = newFilePicker(m.dir), ""
		return m, nil
	}
	switch id := m.home.update(k); {
	case id == "setup":
		return m, m.startSetup()
	case id == "open":
		m.picker, m.invalid = newFilePicker(m.dir), ""
	case strings.HasPrefix(id, "file:"):
		return m, m.check(strings.TrimPrefix(id, "file:"))
	}
	return m, nil
}

func (m firstRunModel) quit() (tea.Model, tea.Cmd) {
	if m.stopping {
		// A second press leaves without waiting for the listener.
		return m, tea.Quit
	}
	m.stopping = true
	m.stop()
	return m, nil
}

// RunFirstRun draws first-run mode. serve is daemon.FirstRunContext with
// the observer this screen hands it; it stops when ctx does. The returned
// Boot is non-nil when the person saved a config and chose to start it:
// the listener has stopped by then, and the caller boots that config.
func RunFirstRun(opts FirstRunOptions, serve func(context.Context, daemon.FirstRunObserver) error) (*Boot, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	// SIGTERM from outside still stops the listener; SIGINT arrives as
	// ctrl+c in raw mode and goes through the screen.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	detect := func() machine {
		dctx, dcancel := context.WithTimeout(ctx, 3*time.Second)
		defer dcancel()
		return detect(dctx, getenv)
	}
	m := newFirstRunModel(opts.Version, time.Now, cancel, detect, opts.Validate, opts.Dir)
	p := tea.NewProgram(m, tea.WithOutput(os.Stdout), tea.WithInput(os.Stdin), tea.WithoutSignalHandler())

	obs := daemon.FirstRunObserver{
		Ready: func(url string, fellBack bool) { p.Send(frReadyMsg{url: url, fellBack: fellBack}) },
		// Send blocks until the screen takes it; the visit is a browser
		// waiting on a redirect, so a slow screen costs it milliseconds.
		Visit: func() { p.Send(frVisitMsg(time.Now())) },
	}
	runErr := make(chan error, 1)
	go func() {
		err := serve(ctx, obs)
		runErr <- err
		p.Send(doneMsg{err: err})
	}()

	final, perr := p.Run()
	cancel()
	var derr error
	select {
	case derr = <-runErr:
	case <-time.After(10 * time.Second):
		derr = errors.New("the first-run listener did not stop within 10s")
	}
	if perr != nil {
		return nil, errors.Join(fmt.Errorf("tui: %w", perr), derr)
	}
	fm, ok := final.(firstRunModel)
	if !ok {
		return nil, derr
	}
	if fm.boot != nil {
		if derr != nil {
			return nil, fmt.Errorf("stopping the first-run listener before boot: %w", derr)
		}
		fmt.Fprintf(os.Stdout, "hoop sidecar: booting %s\n", fm.boot.ConfigPath)
		return fm.boot, nil
	}
	fmt.Fprint(os.Stdout, fm.farewell())
	return nil, derr
}

// farewell is what stays in the scrollback once the screen is gone.
func (m firstRunModel) farewell() string {
	s := fmt.Sprintf("hoop sidecar first run stopped after %s", short(m.now().Sub(m.started)))
	if m.visits > 0 {
		s += fmt.Sprintf(", %d visit(s) to %s", m.visits, m.url)
	}
	s += "\n"
	if m.saved != "" {
		return s + fmt.Sprintf("  saved %s\n  next: hoop start sidecar --config %s\n", m.saved, m.saved)
	}
	return s + "  next: run hoop start sidecar and press w to set up a config\n"
}
