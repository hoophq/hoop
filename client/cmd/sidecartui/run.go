package sidecartui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Options says what the capture needs to know about the daemon's config.
type Options struct {
	// Version is shown in the header.
	Version string
	// AuditFile is the config's audit.file. "" and "-" mean the trail goes
	// to stdout, which the TUI captures; a path is followed like tail -f.
	AuditFile string
	// Reviewer, when set, is the backend the daemon files held statements
	// with (daemon.WithLocalReviewer); the TUI shows each one and records
	// the answer. Operator is who answers, written with each decision.
	Reviewer *Reviewer
	Operator string
}

// Run starts the daemon through run and presents its output in format f.
//
// run must write its logs and audit trail through os.Stdout and os.Stderr as
// read when it starts, which daemon.Run does. JSON does nothing at all: the
// bytes are the daemon's own, the same as before this package existed.
func Run(f Format, opts Options, run func() error) error {
	switch f {
	case FormatTUI:
		return runTUI(opts, run)
	case FormatText:
		return runText(run)
	case FormatJSON:
		return run()
	}
	return fmt.Errorf("unknown log format %q", f)
}

// redirect points os.Stdout or os.Stderr at a pipe and returns the read end
// and a function that puts the original back.
//
// Swapping the variable, not the file descriptor, is what keeps the terminal
// for the TUI: the daemon's writers read the variable when they are built,
// and the TUI keeps the original *os.File. A Go runtime panic still reaches
// fd 2 directly, which is what an operator wants from a crash.
func redirect(target **os.File) (*os.File, func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	orig := *target
	*target = w
	return r, func() {
		*target = orig
		_ = w.Close()
	}, nil
}

// scan feeds each line of r to emit until r ends. Lines can be long: an audit
// event carries the statement, which may be kilobytes.
func scan(r io.Reader, emit func([]byte)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		emit(sc.Bytes())
	}
}

func runText(run func() error) error {
	orig := os.Stderr
	r, restore, err := redirect(&os.Stderr)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan(r, func(line []byte) {
			if rec, ok := ParseLog(line); ok {
				fmt.Fprintln(orig, TextLine(rec))
				return
			}
			fmt.Fprintln(orig, string(line))
		})
	}()
	runErr := run()
	restore()
	<-done
	return runErr
}

func runTUI(opts Options, run func() error) error {
	out, in := os.Stdout, os.Stdin

	// Detect color against the terminal BEFORE stdout becomes a pipe.
	// lipgloss detects lazily, and a detection after the swap would see the
	// pipe and draw in monochrome.
	lipgloss.SetColorProfile(termenv.NewOutput(out).EnvColorProfile())
	lipgloss.SetHasDarkBackground(darkBackground(os.Getenv("COLORFGBG")))

	var notes []string
	errR, restoreErr, err := redirect(&os.Stderr)
	if err != nil {
		return err
	}
	outR, restoreOut, err := redirect(&os.Stdout)
	if err != nil {
		restoreErr()
		return err
	}

	var tail *os.File
	switch opts.AuditFile {
	case "", "-":
		notes = append(notes, "audit trail captured from stdout")
	case os.DevNull:
		notes = append(notes, "audit trail disabled (/dev/null): connections come from the log, statements are not shown")
	default:
		// Opened before the daemon starts so the first event is not
		// missed, and at its end so a long history does not replay.
		f, oerr := os.OpenFile(opts.AuditFile, os.O_RDONLY|os.O_CREATE, 0o640)
		if oerr == nil {
			_, _ = f.Seek(0, io.SeekEnd)
			tail = f
			notes = append(notes, "audit trail followed in "+opts.AuditFile)
		} else {
			notes = append(notes, "audit trail in "+opts.AuditFile+" is not readable: "+oerr.Error())
		}
	}

	now := time.Now
	stop := func() {
		if self, err := os.FindProcess(os.Getpid()); err == nil {
			_ = self.Signal(os.Interrupt)
		}
	}
	m := newModel(opts.Version, notes, now, stop)
	m.reviewer, m.operator = opts.Reviewer, opts.Operator
	if opts.Reviewer != nil {
		notes = append(notes, "held statements are reviewed in this terminal by "+opts.Operator)
		m.notes = notes
	}
	// No mouse capture: an operator selects a session or review id with
	// the mouse to paste it elsewhere, and capturing the mouse takes that
	// away for a scroll wheel the arrow keys already cover.
	p := tea.NewProgram(m, tea.WithAltScreen(),
		tea.WithOutput(out), tea.WithInput(in),
		// The daemon owns SIGINT: a key press in raw mode is not a signal,
		// so q sends it one, and the daemon shuts down the way it always
		// does.
		tea.WithoutSignalHandler())

	// The daemon never waits for the screen. Captured lines go into a
	// bounded inbox without blocking, and a line that finds it full is
	// counted and dropped: the audit trail and the log keep everything,
	// the screen only shows it. Without this, a terminal frozen with Ctrl-S
	// would fill the pipes and stall every connection behind a log write.
	stopTail := make(chan struct{})
	inbox := make(chan tea.Msg, 8192)
	var dropped atomic.Int64
	push := func(msg tea.Msg) {
		select {
		case inbox <- msg:
		default:
			dropped.Add(1)
		}
	}
	go func() {
		for msg := range inbox {
			p.Send(msg)
		}
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var shown int64
		for {
			select {
			case <-stopTail:
				return
			case <-t.C:
				if n := dropped.Load(); n != shown {
					shown = n
					p.Send(droppedMsg(n))
				}
			}
		}
	}()
	emit := func(line []byte) {
		if ev, ok := ParseAudit(line); ok {
			push(auditMsg(ev))
			return
		}
		if rec, ok := ParseLog(line); ok {
			push(logMsg(rec))
			return
		}
		if len(line) > 0 {
			push(rawMsg(string(line)))
		}
	}
	if rv := opts.Reviewer; rv != nil {
		// Reviews are never dropped: each change raises a flag and this
		// loop sends the whole state, so a missed signal is caught by the
		// next one.
		go func() {
			for {
				select {
				case <-stopTail:
					return
				case <-rv.Changed():
					p.Send(localReviewsMsg(rv.Snapshot()))
				}
			}
		}()
	}
	go scan(errR, emit)
	go scan(outR, emit)
	if tail != nil {
		go follow(tail, emit, stopTail)
	}

	runErr := make(chan error, 1)
	go func() {
		err := run()
		runErr <- err
		p.Send(doneMsg{err: err})
	}()

	final, perr := p.Run()
	close(stopTail)

	var derr error
	select {
	case derr = <-runErr:
	default:
		// The TUI left first (a second q, or the terminal closed). Stop
		// the daemon and give it the time a clean shutdown takes.
		stop()
		select {
		case derr = <-runErr:
		case <-time.After(10 * time.Second):
			derr = errors.New("the sidecar did not stop within 10s")
		}
	}
	restoreOut()
	restoreErr()

	if perr != nil {
		return errors.Join(fmt.Errorf("tui: %w", perr), derr)
	}
	if fm, ok := final.(model); ok {
		st := fm.st
		fmt.Fprintf(out, "hoop sidecar stopped after %s: %s statements, %s denied, %s masked, %d warnings\n",
			short(now().Sub(st.Started)), num(st.Statements), num(st.Denied), num(st.Masked), len(st.Warnings))
		// Who approved what: the audit trail has no field for it, so the
		// decisions this terminal made are printed where the scrollback
		// keeps them.
		for _, id := range st.ReviewOrder {
			if r := st.Reviews[id]; r != nil && r.Local && r.DecidedBy != "" {
				verdict := "approved"
				if r.Status == statusRejected {
					verdict = "rejected"
				}
				fmt.Fprintf(out, "  review %s on %s: %s by %s at %s\n", r.ID, r.Lane,
					verdict, r.DecidedBy, r.Decided.Local().Format("15:04:05"))
			}
		}
		// The last errors are what the operator needs once the screen is
		// gone, so they are printed where the scrollback keeps them.
		shown := 0
		for i := len(st.Warnings) - 1; i >= 0 && shown < 5; i-- {
			if st.Warnings[i].Level == "ERROR" {
				fmt.Fprintln(os.Stderr, TextLine(st.Warnings[i]))
				shown++
			}
		}
	}
	return derr
}

// darkBackground reads the background from COLORFGBG ("15;0" is light text
// on black) and assumes dark without it.
//
// It does not ask the terminal. termenv's query (OSC 11) waits up to five
// seconds for an answer that tmux, screen and several terminals never send,
// and the sidecar would sit on a blank screen for that long at every start.
func darkBackground(colorfgbg string) bool {
	parts := strings.Split(colorfgbg, ";")
	bg, err := strconv.Atoi(parts[len(parts)-1])
	if colorfgbg == "" || err != nil {
		return true
	}
	// 0-6 and 8 are the dark ANSI colors; 7 and 9-15 are light.
	return bg < 7 || bg == 8
}

// follow reads lines appended to f until stop closes.
func follow(f *os.File, emit func([]byte), stop <-chan struct{}) {
	defer f.Close()
	rd := bufio.NewReaderSize(f, 64*1024)
	var partial []byte
	for {
		chunk, err := rd.ReadBytes('\n')
		partial = append(partial, chunk...)
		if err == nil {
			emit(partial)
			partial = partial[:0]
			continue
		}
		select {
		case <-stop:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
