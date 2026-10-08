package sidecartui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// FirstRunOptions is what the first-run screen needs from the CLI.
type FirstRunOptions struct {
	Version string
	// Validate checks a written config the way --validate would and
	// returns its one-line summary. The CLI injects it, since it links the
	// YAML loader and the PII plugin and this package does not decide that.
	Validate func(path string) (string, error)
	// Getenv reads the environment for Detect; os.Getenv when nil.
	Getenv func(string) string
}

// Messages first-run mode sends the screen.
type (
	frReadyMsg struct {
		url      string
		fellBack bool
	}
	frVisitMsg   time.Time
	frFrameMsg   time.Time
	frWrittenMsg StarterResult
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

	// write runs the w action off the UI goroutine; nil in tests that
	// drive frWrittenMsg directly.
	write   func() StarterResult
	writing bool
	result  *StarterResult

	stop     func() error
	stopErr  error
	stopping bool
	done     bool
	err      error
}

func newFirstRunModel(version string, now func() time.Time, stop func() error, write func() StarterResult) firstRunModel {
	if version == "" || version == "unknown" {
		version = "dev"
	}
	return firstRunModel{now: now, version: version, started: now(), stop: stop, write: write}
}

func frTick() tea.Cmd {
	return tea.Tick(frFrame, func(t time.Time) tea.Msg { return frFrameMsg(t) })
}

func (m firstRunModel) Init() tea.Cmd { return frTick() }

func (m firstRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case frReadyMsg:
		m.url, m.fellBack, m.ready = msg.url, msg.fellBack, true
	case frVisitMsg:
		m.visits++
		m.lastVisit = time.Time(msg)
		m.pulses = append(m.pulses, time.Time(msg))
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
	case frWrittenMsg:
		r := StarterResult(msg)
		m.writing, m.result = false, &r
	case doneMsg:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m firstRunModel) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c", "esc":
		if m.stopping {
			// A second press leaves without waiting for the server.
			return m, tea.Quit
		}
		m.stopping = true
		if err := m.stop(); err != nil {
			m.stopErr = err
			return m, tea.Quit
		}
	case "w":
		if m.writing || m.write == nil || (m.result != nil && m.result.Path != "") {
			return m, nil
		}
		m.writing = true
		write := m.write
		return m, func() tea.Msg { return frWrittenMsg(write()) }
	}
	return m, nil
}

// RunFirstRun draws first-run mode: serve is daemon.FirstRun with the
// observer this screen hands it, and it blocks until SIGINT, like the
// daemon. q sends that signal; the daemon's handler stops the listener.
func RunFirstRun(opts FirstRunOptions, serve func(daemon.FirstRunObserver) error) error {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	stop := func() error {
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return self.Signal(os.Interrupt)
	}
	write := func() StarterResult {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return WriteStarter(ctx, "", getenv, opts.Validate)
	}
	m := newFirstRunModel(opts.Version, time.Now, stop, write)
	p := tea.NewProgram(m, tea.WithOutput(os.Stdout), tea.WithInput(os.Stdin),
		// The daemon owns SIGINT, as in runTUI.
		tea.WithoutSignalHandler())

	obs := daemon.FirstRunObserver{
		Ready: func(url string, fellBack bool) { p.Send(frReadyMsg{url: url, fellBack: fellBack}) },
		// Send blocks until the screen takes it; the visit is a browser
		// waiting on a redirect, so a slow screen costs it milliseconds.
		Visit: func() { p.Send(frVisitMsg(time.Now())) },
	}
	runErr := make(chan error, 1)
	go func() {
		err := serve(obs)
		runErr <- err
		p.Send(doneMsg{err: err})
	}()

	final, perr := p.Run()
	var derr error
	select {
	case derr = <-runErr:
	default:
		if serr := stop(); serr != nil {
			derr = fmt.Errorf("the first-run listener could not be stopped: %w", serr)
			break
		}
		select {
		case derr = <-runErr:
		case <-time.After(10 * time.Second):
			derr = errors.New("the first-run listener did not stop within 10s")
		}
	}
	if perr != nil {
		return errors.Join(fmt.Errorf("tui: %w", perr), derr)
	}
	if fm, ok := final.(firstRunModel); ok {
		fmt.Fprint(os.Stdout, fm.farewell())
	}
	return derr
}

// farewell is what stays in the scrollback once the screen is gone: the
// file that was written and the one command to run next.
func (m firstRunModel) farewell() string {
	s := fmt.Sprintf("hoop sidecar first run stopped after %s", short(m.now().Sub(m.started)))
	if m.visits > 0 {
		s += fmt.Sprintf(", %d visit(s) to %s", m.visits, m.url)
	}
	s += "\n"
	switch r := m.result; {
	case r != nil && r.Path != "":
		s += fmt.Sprintf("  wrote %s\n  next: hoop start sidecar --config %s\n", r.Path, r.Path)
	default:
		s += "  next: write a config and run hoop start sidecar --config config.yaml\n"
	}
	return s
}
