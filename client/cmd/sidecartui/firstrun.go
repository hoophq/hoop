package sidecartui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	// ConnectCheck connects to a Control Plane the way the boot that
	// follows will (the handshake included) and returns the config the
	// plane serves. The CLI injects it, as it does Validate.
	ConnectCheck func(planeURL, token string) (*daemon.Config, error)
	// OpenURL opens a link in the person's browser; nil hides the option.
	OpenURL func(string) error
	// LicenseDir is where a license entered on the Connect page is saved.
	LicenseDir string
	// UseLicense tells the CLI a license was saved at path, so what it
	// validates and boots from here on runs under it.
	UseLicense func(path string)
	// ManagedURL is the hoop-managed control plane. Set, the screen opens
	// on one choice: log in there (with ManagedLogin, on the screen), or
	// Enterprise (the Connect page). Empty opens the Get started list.
	ManagedURL   string
	ManagedLogin ManagedLogin
	// Connected is a hoop-managed login this sidecar already holds, with no
	// config yet: the screen opens on Get started, connected to it.
	Connected *ManagedPlane
}

// ManagedPlane is this sidecar's login to the hoop-managed control plane.
// Page is the sidecar's page in the web app.
type ManagedPlane struct {
	URL, Token, Name, Page string
}

// LoginTicket is what the person approves in the browser: the link, which
// carries the code, and the code itself, shown so they can match it.
type LoginTicket struct {
	URL, Code string
}

// ManagedLogin is the device-code login, in two steps so the screen can show
// the ticket while it waits. The CLI implements it and saves the login.
type ManagedLogin interface {
	Begin(ctx context.Context) (LoginTicket, error)
	Wait(ctx context.Context) (ManagedPlane, error)
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
		// conflicts are ports the config binds that another program
		// holds; the boot waits on the person's answer.
		conflicts []portConflict
	}
	// frConnectedMsg is a Control Plane connection's check: nil err and
	// no conflicts boots on it.
	frConnectedMsg struct {
		planeURL, token string
		err             error
		conflicts       []string
		// empty is a plane that took the token and holds no config yet.
		empty bool
	}
	// frTicketMsg is a started login; frLoggedInMsg its approval.
	frTicketMsg struct {
		ticket LoginTicket
		err    error
	}
	frLoggedInMsg struct {
		plane ManagedPlane
		err   error
	}
	// frProbeTickMsg asks the plane again; frPlaneReadyMsg is its answer:
	// ready when the plane holds a configuration for this sidecar.
	frProbeTickMsg  struct{}
	frPlaneReadyMsg struct{ ready bool }
)

// planeProbeEvery is how often a logged-in screen asks the plane whether the
// web app gave this sidecar a configuration.
const planeProbeEvery = 4 * time.Second

func probeTick() tea.Cmd {
	return tea.Tick(planeProbeEvery, func(time.Time) tea.Msg { return frProbeTickMsg{} })
}

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
	// ports is the "port in use" question while it is open; portErr is a
	// port fix that could not be written.
	ports   *portAsk
	portErr string

	// connect is the Connect to a Control Plane page while it is open.
	connect      *connectPage
	connectCheck func(planeURL, token string) (*daemon.Config, error)
	openURL      func(string) error
	licenseDir   string
	useLicense   func(path string)
	// licensePath is the license saved on the Connect page, and
	// licenseNote what it is; the configs set up from here carry it.
	licensePath string
	// plane is a connected Control Plane waiting for its first config,
	// nil otherwise; the config booted next goes to it.
	plane       *pendingPlane
	licenseNote string

	// managedURL is the hoop-managed control plane offered first; choosing
	// is set while the screen shows that choice instead of Get started.
	managedURL string
	choosing   bool
	// managedLogin runs the login; login is its page while it is open, and
	// loginCancel stops a login in flight.
	managedLogin ManagedLogin
	login        *loginView
	loginCancel  context.CancelFunc
	// managed is the hoop-managed login this sidecar holds. Unlike plane, it
	// stays for every boot from here: the sidecar is logged in, whichever
	// config it boots.
	managed *ManagedPlane
	// probing is a plane check in flight; planeReady is the plane holding a
	// configuration, which the screen then boots on.
	probing    bool
	planeReady bool

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
	if m.choosing {
		m.home.items = []menuItem{
			{id: "managed", label: "Managed by hoop (free)",
				detail: "log in at " + hostOf(m.managedURL) + " · free: 1 sidecar, 1 rule per feature"},
			{id: "enterprise", label: "Enterprise",
				detail: "your own Control Plane and token, or a license"},
		}
		m.home.cur = min(m.home.cur, len(m.home.items)-1)
		return
	}
	items := []menuItem{
		{id: "setup", label: "Set up a config file", detail: "pick the demo or a protocol, adjust the defaults, boot"},
		{id: "connect", label: "Connect to a Control Plane", detail: "Enterprise: manage this sidecar from the hoop web app, or start with a license"},
		{id: "open", label: "Open a config file", detail: "browse to a config you already have and boot it"},
		{note: true, label: "Config files in this folder"},
	}
	if m.managed != nil {
		// Logged in already: no Connect, and the file is where the setup ends.
		items = []menuItem{
			{id: "setup", label: "Set up a config file", detail: "pick the demo or a protocol, adjust the defaults, boot; the file stays on this machine"},
			{id: "open", label: "Open a config file", detail: "browse to a config you already have and boot it"},
			{note: true, label: "Config files in this folder"},
		}
	}
	files := configsIn(m.dir)
	for _, f := range files {
		label := clean(filepath.Base(f))
		if f == m.invalid {
			items = append(items, menuItem{id: "file:" + f, label: label, detail: "not a valid sidecar config"})
			continue
		}
		items = append(items, menuItem{id: "file:" + f, label: label, detail: "boot the sidecar with it"})
	}
	if len(files) == 0 {
		items = append(items, menuItem{note: true, label: "No config in this directory"})
	}
	if m.managed != nil {
		items = append(items, menuItem{note: true, label: "Prefer the web app?"},
			menuItem{id: "hoopconfig", label: "Configure in hoop instead",
				detail: "boot with no file and add its listeners and rules at " + hostOf(m.managed.URL)})
	}
	cur := m.home.cur
	m.home.items = items
	m.home.cur = min(cur, len(items)-1)
	if m.home.items[m.home.cur].note {
		m.home.cur = 0
	}
}

// check readies path for a boot, off the UI goroutine: it validates the
// config, then tries every port it binds, because a valid config whose
// port another program holds still fails the moment it boots.
func (m *firstRunModel) check(path string) tea.Cmd {
	m.checking, m.invalid, m.portErr = path, "", ""
	validate := m.validate
	return func() tea.Msg {
		if validate == nil {
			return frCheckedMsg{path: path, err: errors.New("this build has no validator")}
		}
		if _, err := validate(path); err != nil {
			return frCheckedMsg{path: path, err: err}
		}
		conflicts, err := portConflicts(path)
		return frCheckedMsg{path: path, err: err, conflicts: conflicts}
	}
}

// usePortsAndRetry moves the config to the offered ports and checks it
// again, so what boots is what was checked.
func (m *firstRunModel) usePortsAndRetry() tea.Cmd {
	path, conflicts := m.ports.path, m.ports.conflicts
	m.ports = nil
	if err := usePorts(path, conflicts); err != nil {
		if m.wiz != nil {
			m.wiz.saveErr = fmt.Errorf("not booted: %w", err)
		} else {
			m.portErr = err.Error()
		}
		return nil
	}
	return m.check(path)
}

// connectKey drives the Connect page: Continue with the chosen way to
// unlock, Talk to us, or Back.
func (m firstRunModel) connectKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.connect
	if p.busy != "" {
		return m, nil
	}
	cmd, ev := p.form.update(k)
	switch ev {
	case "back":
		m.connect = nil
		// Enterprise was a choice on the first screen: back is that screen.
		m.choosing = m.managedURL != ""
		m.buildHome()
	case "meet":
		p.status, p.bad = "Opened "+MeetURL+" in your browser.", false
		if m.openURL == nil {
			p.status = "Open " + MeetURL + " in your browser to talk to us."
		} else if err := m.openURL(MeetURL); err != nil {
			p.status, p.bad = "Could not open a browser; go to "+MeetURL+" to talk to us.", true
		}
	case "continue":
		if p.unlock() == unlockLicense {
			return m.continueWithLicense()
		}
		planeURL, token, err := p.planeInput()
		if err != nil {
			p.status, p.bad = "✕ "+err.Error(), true
			return m, cmd
		}
		if m.connectCheck == nil {
			p.status, p.bad = "✕ this build cannot connect to a Control Plane", true
			return m, cmd
		}
		host := planeURL
		if u, err := url.Parse(planeURL); err == nil {
			host = u.Host
		}
		p.busy, p.status = "connecting to "+host+"…", ""
		check := m.connectCheck
		return m, func() tea.Msg {
			cfg, err := check(planeURL, token)
			msg := frConnectedMsg{planeURL: planeURL, token: token, err: err}
			if errors.Is(err, daemon.ErrPlaneHasNoConfig) {
				msg.err, msg.empty = nil, true
			} else if err == nil {
				msg.conflicts = connectConflicts(cfg)
			}
			return msg
		}
	}
	return m, cmd
}

// paste sends pasted text to the field on screen: the Connect page's URL,
// token or license, a setup form, or the file picker's path. A paste while a
// dialog or a check is up is dropped, as a key would be.
func (m firstRunModel) paste(p tea.PasteMsg) tea.Cmd {
	switch {
	case m.ports != nil || m.checking != "":
		return nil
	case m.wiz != nil:
		cmd, _ := m.wiz.update(p)
		return cmd
	case m.connect != nil:
		if m.connect.busy != "" {
			return nil
		}
		return m.connect.form.paste(p)
	case m.picker != nil:
		m.picker.paste(p)
	}
	return nil
}

// continueWithLicense checks and saves the license, then leads on to
// setting up a config that runs under it.
func (m firstRunModel) continueWithLicense() (tea.Model, tea.Cmd) {
	p := m.connect
	p.form.store()
	path, st, err := saveLicense(p.form.byID("license").text, m.licenseDir)
	if err != nil {
		p.status, p.bad = "✕ "+err.Error(), true
		return m, nil
	}
	m.licensePath, m.licenseNote = path, licenseLine(st)
	if m.useLicense != nil {
		m.useLicense(path)
	}
	m.connect, m.plane = nil, nil
	return m, m.startSetup()
}

// loginView is the login page: the ticket once the plane gave one, and the
// last failure.
type loginView struct {
	ticket LoginTicket
	opened bool
	err    string
	ctx    context.Context
}

// startLogin opens the login page and asks the plane for a code.
func (m firstRunModel) startLogin() (tea.Model, tea.Cmd) {
	if m.managedLogin == nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.login, m.loginCancel = &loginView{ctx: ctx}, cancel
	ml := m.managedLogin
	return m, func() tea.Msg {
		t, err := ml.Begin(ctx)
		return frTicketMsg{ticket: t, err: err}
	}
}

func (m firstRunModel) loginCtx() context.Context { return m.login.ctx }

// endLogin closes the login page and stops a login in flight.
func (m *firstRunModel) endLogin() {
	if m.loginCancel != nil {
		m.loginCancel()
	}
	m.login, m.loginCancel = nil, nil
}

// loginError is a failed login as one line; a cancel is not a failure.
func loginError(err error) string {
	if errors.Is(err, context.Canceled) {
		return ""
	}
	return firstLine(err)
}

// hostOf is the host a person recognizes in a URL: use.hoop.dev, not the
// scheme and path around it.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// pendingPlane is a Control Plane connected on the Connect page that holds
// no config yet: the config set up next is booted on it. The sidecar runs
// that file and reports it; the plane takes it only when an admin moves it.
type pendingPlane struct{ url, token string }

// portAsk is the dialog that offers free ports for the ones in use.
type portAsk struct {
	path      string
	conflicts []portConflict
	// yes is the focused answer: use the free ports.
	yes bool
}

// fixable is whether every conflict has a free port to offer.
func (p *portAsk) fixable() bool {
	for _, c := range p.conflicts {
		if c.free == "" {
			return false
		}
	}
	return true
}

// portKey answers the dialog: use the free ports and boot, or go back.
func (m firstRunModel) portKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m.quit()
	case "left", "right", "tab", "shift+tab", "h", "l":
		if m.ports.fixable() {
			m.ports.yes = !m.ports.yes
		}
	case "y":
		if m.ports.fixable() {
			return m, m.usePortsAndRetry()
		}
	case "n", "esc":
		m.ports = nil
	case "enter", "space":
		if m.ports.yes && m.ports.fixable() {
			return m, m.usePortsAndRetry()
		}
		m.ports = nil
	}
	if m.ports == nil && m.wiz != nil {
		m.wiz.saveErr = errors.New("not booted: a port it listens on is in use. Change it under Listener, then boot again")
	}
	return m, nil
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

func (m firstRunModel) Init() tea.Cmd {
	// See model.Init: ask for the real terminal background so the palette
	// picked from COLORFGBG at startup is corrected once the terminal answers.
	if m.managed != nil {
		return tea.Batch(frTick(), probeTick(), tea.RequestBackgroundColor)
	}
	return tea.Batch(frTick(), tea.RequestBackgroundColor)
}

func (m firstRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		setTheme(msg.IsDark())
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
		m.wiz.license = m.licensePath
		if m.plane != nil {
			m.wiz.plane = m.plane.url
		} else if m.managed != nil {
			m.wiz.plane = m.managed.URL
		}
		return m, nil
	case doneMsg:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case frConnectedMsg:
		p := m.connect
		if p == nil {
			return m, nil
		}
		p.busy = ""
		switch {
		case msg.err != nil:
			p.status, p.bad = "✕ Could not connect: "+firstLine(msg.err), true
		case msg.empty:
			// Connected, and the plane holds no config: set one up and boot
			// it. The plane shows it and takes it only on an admin's action.
			m.connect, m.plane = nil, &pendingPlane{url: msg.planeURL, token: msg.token}
			return m, m.startSetup()
		case len(msg.conflicts) > 0:
			p.status, p.bad = "✕ Connected, but "+strings.Join(msg.conflicts, "; ")+
				". Change it in the Control Plane, or stop that program, then connect again.", true
		default:
			m.boot = &Boot{ControlPlaneURL: msg.planeURL, Token: msg.token}
			return m, tea.Quit
		}
		return m, nil
	case frTicketMsg:
		if m.login == nil {
			return m, nil // the person went back meanwhile
		}
		if msg.err != nil {
			m.login.err = loginError(msg.err)
			return m, nil
		}
		m.login.ticket = msg.ticket
		if m.openURL != nil {
			m.login.opened = m.openURL(msg.ticket.URL) == nil
		}
		ml, ctx := m.managedLogin, m.loginCtx()
		return m, func() tea.Msg {
			plane, err := ml.Wait(ctx)
			return frLoggedInMsg{plane: plane, err: err}
		}
	case frLoggedInMsg:
		if m.login == nil {
			return m, nil
		}
		if msg.err != nil {
			m.login.err = loginError(msg.err)
			return m, nil
		}
		m.endLogin()
		plane := msg.plane
		m.managed, m.choosing = &plane, false
		m.home.cur = 0
		m.buildHome()
		return m, probeTick()
	case frProbeTickMsg:
		// Configured in the web app instead: the plane now serves this
		// sidecar a configuration, and the screen boots on it, as a
		// Save and boot here would. The check is the Connect page's own.
		if m.managed == nil || m.boot != nil || m.connectCheck == nil {
			return m, nil
		}
		if m.probing || m.checking != "" {
			return m, probeTick()
		}
		m.probing = true
		check, url, token := m.connectCheck, m.managed.URL, m.managed.Token
		return m, func() tea.Msg {
			_, err := check(url, token)
			return frPlaneReadyMsg{ready: err == nil}
		}
	case frPlaneReadyMsg:
		m.probing = false
		if !msg.ready || m.managed == nil || m.boot != nil || m.checking != "" {
			return m, probeTick()
		}
		m.planeReady = true
		m.boot = &Boot{ControlPlaneURL: m.managed.URL, Token: m.managed.Token}
		return m, tea.Quit
	case frCheckedMsg:
		if msg.path != m.checking {
			return m, nil
		}
		m.checking = ""
		if msg.err != nil {
			if m.wiz != nil {
				// The setup screens validated this file before saving it;
				// say what changed under them, where the person is.
				m.wiz.saveErr = fmt.Errorf("not booted: %w", msg.err)
				return m, nil
			}
			// Not the validator's text: the person chose a file to run,
			// and the answer is that it cannot be. The reason is one
			// command away, named on the screen.
			m.invalid = msg.path
			m.buildHome()
			return m, nil
		}
		if len(msg.conflicts) > 0 {
			m.ports = &portAsk{path: msg.path, conflicts: msg.conflicts, yes: true}
			return m, nil
		}
		m.picker = nil
		m.boot = &Boot{ConfigPath: msg.path}
		if m.plane != nil {
			m.boot.ControlPlaneURL, m.boot.Token = m.plane.url, m.plane.token
		} else if m.managed != nil {
			m.boot.ControlPlaneURL, m.boot.Token = m.managed.URL, m.managed.Token
		}
		return m, tea.Quit
	}
	if p, ok := msg.(tea.PasteMsg); ok {
		return m, m.paste(p)
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if m.ports != nil {
			return m.portKey(k)
		}
		// A boot is being checked: a key now would act on a screen that
		// is about to go away.
		if m.checking != "" && k.String() != "ctrl+c" {
			return m, nil
		}
	}
	if m.wiz != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "ctrl+c" {
			return m.quit()
		}
		cmd, ev := m.wiz.update(msg)
		switch ev {
		case wizExit:
			// Leaving the setup drops the pending plane: a file opened
			// from the list later must not connect by surprise.
			m.wiz, m.plane = nil, nil
			m.buildHome()
		case wizSavedOnly:
			// The pending plane went with the setup: the saved file is
			// booted later from the list, on its own, unless it says otherwise.
			m.saved, m.wiz, m.plane = m.wiz.saved, nil, nil
			m.buildHome()
		case wizBoot:
			// Saved and valid; the ports are checked like any other boot.
			return m, m.check(m.wiz.saved)
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
	if m.connect != nil {
		return m.connectKey(k)
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
	if m.login != nil {
		switch k.String() {
		case "esc", "q":
			// Back to the choice; the code is left to expire.
			m.endLogin()
			m.buildHome()
		case "o":
			if m.openURL != nil && m.login.ticket.URL != "" {
				m.login.opened = m.openURL(m.login.ticket.URL) == nil
			}
		}
		return m, nil
	}
	if m.choosing {
		switch k.String() {
		case "q", "esc":
			return m.quit()
		}
		switch m.home.update(k) {
		case "managed":
			return m.startLogin()
		case "enterprise":
			m.choosing = false
			m.connect, m.invalid = newConnectPage(m.licenseDir), ""
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
	case id == "connect":
		m.connect, m.invalid = newConnectPage(m.licenseDir), ""
	case id == "open":
		m.picker, m.invalid = newFilePicker(m.dir), ""
	case id == "hoopconfig" && m.managed != nil:
		// No file: the sidecar boots on the plane and waits there for its
		// listeners, and the web app is where they are added.
		m.boot = &Boot{ControlPlaneURL: m.managed.URL, Token: m.managed.Token}
		if m.openURL != nil && m.managed.Page != "" {
			_ = m.openURL(m.managed.Page)
		}
		return m, tea.Quit
	case strings.HasPrefix(id, "file:"):
		return m, m.check(strings.TrimPrefix(id, "file:"))
	}
	return m, nil
}

func (m firstRunModel) quit() (tea.Model, tea.Cmd) {
	m.endLogin()
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
	m.connectCheck, m.openURL, m.licenseDir, m.useLicense = opts.ConnectCheck, opts.OpenURL, opts.LicenseDir, opts.UseLicense
	m.managedLogin = opts.ManagedLogin
	switch {
	case opts.Connected != nil:
		m.managed = opts.Connected
		m.buildHome()
	case opts.ManagedURL != "":
		m.managedURL, m.choosing = opts.ManagedURL, true
		m.buildHome()
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stdout), tea.WithInput(os.Stdin), tea.WithoutSignalHandler())

	// The listener's callbacks never wait on the screen. Program.Send blocks
	// until the screen takes the message, and once the screen has quit (to
	// boot a config) nothing takes it: a visit in flight would hold its HTTP
	// handler, the listener's shutdown would time out, and the boot would be
	// lost. Messages go through a buffer a separate goroutine drains; a
	// visit that finds it full is dropped, as it only feeds an animation.
	screenDone := make(chan struct{})
	toScreen := make(chan tea.Msg, 64)
	go func() {
		for {
			select {
			case <-screenDone:
				return
			case msg := <-toScreen:
				p.Send(msg)
			}
		}
	}()
	offer := func(msg tea.Msg) {
		select {
		case toScreen <- msg:
		default:
		}
	}
	obs := daemon.FirstRunObserver{
		Ready: func(url string, fellBack bool) { offer(frReadyMsg{url: url, fellBack: fellBack}) },
		Visit: func() { offer(frVisitMsg(time.Now())) },
	}
	runErr := make(chan error, 1)
	go func() {
		err := serve(ctx, obs)
		runErr <- err
		p.Send(doneMsg{err: err})
	}()

	final, perr := p.Run()
	close(screenDone)
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
		switch {
		case fm.planeReady:
			fmt.Fprintf(os.Stdout, "hoop sidecar: %q runs the configuration set in hoop at %s\n  next time: hoop start sidecar\n",
				fm.managed.Name, fm.managed.URL)
		case fm.managed != nil && fm.boot.ConfigPath == "":
			fmt.Fprintf(os.Stdout, "hoop sidecar: %q waits for its configuration from %s\n", fm.managed.Name, fm.managed.URL)
		case fm.managed != nil:
			fmt.Fprintf(os.Stdout, "hoop sidecar: booting %s as %q, connected to %s\n  next time: hoop start sidecar\n",
				fm.boot.ConfigPath, fm.managed.Name, fm.managed.URL)
		case fm.boot.ControlPlaneURL != "":
			fmt.Fprintf(os.Stdout, "hoop sidecar: connecting to %s\n", fm.boot.ControlPlaneURL)
			fmt.Fprintf(os.Stdout, "  to reconnect later: %s=%s hoop start sidecar --token <your token>\n",
				daemon.ControlPlaneURLEnv, fm.boot.ControlPlaneURL)
		default:
			fmt.Fprintf(os.Stdout, "hoop sidecar: booting %s\n", fm.boot.ConfigPath)
		}
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
