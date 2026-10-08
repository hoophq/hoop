package sidecartui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// maxLine bounds one captured line on the screen. An audit event carries its
// statement, so kilobytes are normal; a line past this is cut for display and
// the rest drained, never left in the pipe where it would stall the daemon.
// The saved copy (Options.SaveDir) keeps every byte.
const maxLine = 1 << 20

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
	// SaveDir is where the TUI keeps a copy of what it captures: the audit
	// trail when it goes to stdout, and the operational log. Without it,
	// both exist only on the screen and are gone at exit, so the CLI always
	// sets it (~/.hoop/sidecar); empty is for tests.
	SaveDir string
	// Notes are facts the caller set up around the daemon (the demo API),
	// shown with the capture's own on the System section.
	Notes []string
	// Demo, when set, adds the Try it section and opens the dashboard on
	// it: the sidecar fronts the demo API.
	Demo *DemoOptions
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
		for _, n := range opts.Notes {
			fmt.Fprintln(os.Stderr, n)
		}
		return runText(run)
	case FormatJSON:
		return run()
	}
	return fmt.Errorf("unknown log format %q", f)
}

// redirect points os.Stdout or os.Stderr at a pipe and returns the read end,
// the write end the daemon now writes through, and a function that puts the
// original back.
//
// Swapping the variable, not the file descriptor, is what keeps the terminal
// for the TUI: the daemon's writers read the variable when they are built,
// and the TUI keeps the original *os.File. A Go runtime panic still reaches
// fd 2 directly, which is what an operator wants from a crash.
func redirect(target **os.File) (*os.File, *os.File, func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	orig := *target
	*target = w
	return r, w, func() {
		*target = orig
		_ = w.Close()
	}, nil
}

// readLines feeds each line of r to emit until r ends, and returns the error
// that ended it, nil at EOF.
//
// It never stops early. A line longer than max is cut to max for emit, with
// truncated set, and the rest of it is read and discarded: a reader that gave
// up on a long line would leave the pipe full and stall the daemon's next
// write, which is a stalled connection.
func readLines(r io.Reader, max int, emit func(line []byte, truncated bool)) error {
	rd := bufio.NewReaderSize(r, 64*1024)
	buf := lineBuf{max: max}
	for {
		chunk, err := rd.ReadSlice('\n')
		buf.add(chunk)
		switch {
		case err == nil:
			buf.flush(emit)
		case errors.Is(err, bufio.ErrBufferFull):
			// A long line: keep reading its next piece.
		case errors.Is(err, io.EOF):
			buf.flush(emit)
			return nil
		default:
			buf.flush(emit)
			return err
		}
	}
}

// lineBuf assembles one line from the pieces a bufio.Reader returns, keeping
// at most max bytes of it and remembering whether more were discarded.
type lineBuf struct {
	line      []byte
	truncated bool
	max       int
}

func (b *lineBuf) add(chunk []byte) {
	room := b.max - len(b.line)
	if len(chunk) > room {
		b.line = append(b.line, chunk[:max(room, 0)]...)
		// The newline that ends a line exactly max long is no loss.
		if len(trimNewline(chunk[max(room, 0):])) > 0 {
			b.truncated = true
		}
		return
	}
	b.line = append(b.line, chunk...)
}

// flush hands a non-empty line to emit and starts the next. emit must not keep
// the slice: it is reused.
func (b *lineBuf) flush(emit func([]byte, bool)) {
	if line := trimNewline(b.line); len(line) > 0 {
		emit(line, b.truncated)
	}
	b.reset()
}

func (b *lineBuf) reset() { b.line, b.truncated = b.line[:0], false }

func trimNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

// keepGoing is a writer that never fails its caller. It records the first
// error and drops what follows: the copy it feeds must never stop the reader
// that drains the daemon's pipe.
type keepGoing struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func (k *keepGoing) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err == nil {
		if _, err := k.w.Write(p); err != nil {
			k.err = err
		}
	}
	return len(p), nil
}

func (k *keepGoing) Err() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.err
}

func runText(run func() error) error {
	orig := os.Stderr
	r, _, restore, err := redirect(&os.Stderr)
	if err != nil {
		return err
	}
	// A failed terminal write is recorded, not fatal: the pipe keeps being
	// drained, or the daemon would block on its next log line.
	out := &keepGoing{w: orig}
	var readErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		readErr = readLines(r, maxLine, func(line []byte, truncated bool) {
			if rec, ok := ParseLog(line); ok && !truncated {
				fmt.Fprintln(out, TextLine(rec))
				return
			}
			fmt.Fprintln(out, string(line))
		})
	}()
	runErr := run()
	restore()
	<-done
	if werr := out.Err(); werr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("writing the log: %w", werr))
	}
	if readErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("reading the log: %w", readErr))
	}
	return runErr
}

// saveFile opens a new file in dir for one captured stream. The name carries
// the start time, so two runs never write into one file.
func saveFile(dir, kind string, started time.Time) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, fmt.Sprintf("%s-%s.jsonl", kind, started.Format("20060102-150405")))
	return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func runTUI(opts Options, run func() error) error {
	out, in := os.Stdout, os.Stdin
	started := time.Now()

	var notes, saved []string
	errR, errW, restoreErr, err := redirect(&os.Stderr)
	if err != nil {
		return err
	}
	outR, _, restoreOut, err := redirect(&os.Stdout)
	if err != nil {
		restoreErr()
		return err
	}

	// What the screen shows is gone at exit, so the captured streams are
	// copied to files first, byte for byte. The audit trail is copied only
	// when it goes to stdout; a configured audit.file already keeps it.
	auditToStdout := opts.AuditFile == "" || opts.AuditFile == "-"
	var copies []*keepGoing
	tee := func(r io.Reader, kind string) io.Reader {
		if opts.SaveDir == "" {
			return r
		}
		f, ferr := saveFile(opts.SaveDir, kind, started)
		if ferr != nil {
			notes = append(notes, "the "+kind+" is NOT saved: "+ferr.Error())
			return r
		}
		k := &keepGoing{w: f}
		copies = append(copies, k)
		saved = append(saved, f.Name())
		notes = append(notes, "the "+kind+" is saved to "+f.Name())
		return io.TeeReader(r, k)
	}
	var stdoutSrc io.Reader = outR
	if auditToStdout {
		stdoutSrc = tee(outR, "audit")
	}
	stderrSrc := tee(errR, "log")
	if opts.SaveDir == "" && auditToStdout {
		notes = append(notes, "the audit trail is NOT saved: set audit.file to keep it")
	}

	var tail *os.File
	switch {
	case auditToStdout:
		notes = append(notes, "audit trail captured from stdout")
	case opts.AuditFile == os.DevNull:
		notes = append(notes, "audit trail disabled (/dev/null): connections come from the log, statements are not shown")
	default:
		// Opened before the daemon starts so the first event is not
		// missed, and at its end so a long history does not replay. A
		// file that cannot be positioned is not followed at all: reading
		// it from the start would replay history as live traffic.
		f, oerr := os.OpenFile(opts.AuditFile, os.O_RDONLY|os.O_CREATE, 0o640)
		switch {
		case oerr != nil:
			notes = append(notes, "audit trail in "+opts.AuditFile+" is not readable: "+oerr.Error())
		default:
			if _, serr := f.Seek(0, io.SeekEnd); serr != nil {
				_ = f.Close()
				notes = append(notes, "audit trail in "+opts.AuditFile+" is not followed: "+serr.Error())
			} else {
				tail = f
				notes = append(notes, "audit trail followed in "+opts.AuditFile)
			}
		}
	}

	stop := func() error {
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return self.Signal(os.Interrupt)
	}
	m := newModel(opts.Version, append(opts.Notes, notes...), time.Now, stop)
	m.reviewer, m.operator = opts.Reviewer, opts.Operator
	if opts.Demo != nil {
		// The demo opens on its guide: a person who just booted it has
		// not sent anything yet, and the empty Wire tells them nothing.
		m.tour = newTour(opts.Demo)
		m.tab, m.menuFocus = tabTour, false
	}
	if opts.Reviewer != nil {
		m.notes = append(m.notes, "held statements wait for approval in this terminal, decided by "+opts.Operator)
		// The decision goes into the daemon's own log stream, as JSON
		// like every other line there: saved with it, shown in Logs, and
		// not only on a screen that a closed terminal takes with it.
		decisions := slog.New(slog.NewJSONHandler(errW, nil))
		m.record = func(msg string, args ...any) { decisions.Info(msg, args...) }
	}
	// No mouse capture: an operator selects a session or approval id with
	// the mouse to paste it elsewhere, and capturing the mouse takes that
	// away for a scroll wheel the arrow keys already cover.
	p := tea.NewProgram(m,
		tea.WithOutput(out), tea.WithInput(in),
		// The daemon owns SIGINT: a key press in raw mode is not a signal,
		// so q sends it one, and the daemon shuts down the way it always
		// does.
		tea.WithoutSignalHandler())

	// The daemon never waits for the screen. Captured lines go into a
	// bounded inbox without blocking, and a line that finds it full is
	// counted and dropped: the saved copy keeps it, the screen only shows
	// it. Without this, a terminal frozen with Ctrl-S would fill the pipes
	// and stall every connection behind a log write.
	done := make(chan struct{})
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
		for {
			select {
			case <-done:
				return
			case msg := <-inbox:
				p.Send(msg)
			}
		}
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var shown int64
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if n := dropped.Load(); n != shown {
					shown = n
					p.Send(droppedMsg(n))
				}
				if rv := opts.Reviewer; rv != nil {
					rv.expire()
				}
			}
		}
	}()
	emit := func(line []byte, truncated bool) {
		if !truncated {
			if ev, ok := ParseAudit(line); ok {
				push(auditMsg(ev))
				return
			}
			if rec, ok := ParseLog(line); ok {
				push(logMsg(rec))
				return
			}
		}
		if len(line) > 0 {
			text := string(line)
			if truncated {
				text += " … (line cut for the screen)"
			}
			push(rawMsg(text))
		}
	}
	if rv := opts.Reviewer; rv != nil {
		// Approvals are never dropped: each change raises a flag and this
		// loop sends the whole state, so a missed signal is caught by the
		// next one.
		go func() {
			for {
				select {
				case <-done:
					return
				case <-rv.Changed():
					p.Send(localReviewsMsg(rv.Snapshot()))
				}
			}
		}()
	}
	drain := func(name string, r io.Reader) {
		if err := readLines(r, maxLine, emit); err != nil {
			push(rawMsg("reading the " + name + " stopped: " + err.Error()))
		}
	}
	go drain("log", stderrSrc)
	go drain("audit trail", stdoutSrc)
	if tail != nil {
		go func() {
			if err := follow(tail, maxLine, emit, done); err != nil {
				push(rawMsg("following " + opts.AuditFile + " stopped: " + err.Error()))
			}
		}()
	}

	runErr := make(chan error, 1)
	go func() {
		err := run()
		runErr <- err
		p.Send(doneMsg{err: err})
	}()

	final, perr := p.Run()
	close(done)

	var derr error
	select {
	case derr = <-runErr:
	default:
		// The TUI left first (a second q, or the terminal closed). Stop
		// the daemon and give it the time a clean shutdown takes. A stop
		// that cannot be sent is reported at once rather than after the
		// wait.
		if serr := stop(); serr != nil {
			derr = fmt.Errorf("the sidecar could not be stopped: %w", serr)
			break
		}
		select {
		case derr = <-runErr:
		case <-time.After(10 * time.Second):
			derr = errors.New("the sidecar did not stop within 10s")
		}
	}
	restoreOut()
	restoreErr()
	for _, k := range copies {
		if werr := k.Err(); werr != nil {
			derr = errors.Join(derr, fmt.Errorf("saving the captured output: %w", werr))
		}
	}

	if perr != nil {
		return errors.Join(fmt.Errorf("tui: %w", perr), derr)
	}
	if fm, ok := final.(model); ok {
		st := fm.st
		fmt.Fprintf(out, "hoop sidecar stopped after %s: %s statements, %s denied, %s masked, %d warnings\n",
			short(time.Since(started)), num(st.Statements), num(st.Denied), num(st.Masked), len(st.Warnings))
		for _, name := range saved {
			fmt.Fprintf(out, "  saved %s\n", name)
		}
		// Who approved what, again where the scrollback keeps it. The
		// saved log has it too (record).
		for _, id := range st.ReviewOrder {
			if r := st.Reviews[id]; r != nil && r.Local && r.DecidedBy != "" {
				verdict := "approved"
				if r.Status == statusRejected {
					verdict = "rejected"
				}
				fmt.Fprintf(out, "  approval %s on %s: %s by %s at %s\n", r.ID, r.Lane,
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
// It does not ask the terminal. A query (OSC 11) waits up to five seconds for
// an answer that tmux, screen and several terminals never send.
func darkBackground(colorfgbg string) bool {
	parts := strings.Split(colorfgbg, ";")
	bg, err := strconv.Atoi(parts[len(parts)-1])
	if colorfgbg == "" || err != nil {
		return true
	}
	// 0-6 and 8 are the dark ANSI colors; 7 and 9-15 are light.
	return bg < 7 || bg == 8
}

// follow reads lines appended to f until stop closes, and returns the error
// that ended it, nil when stop did.
//
// At EOF it waits for more. A file that shrank was truncated in place (a
// copy-and-truncate rotation), and the daemon now appends from its start, so
// reading resumes there. A line longer than max is cut for emit and the rest
// discarded, so an unterminated record cannot grow memory without bound. Any
// other read error ends the follow and is reported.
func follow(f *os.File, max int, emit func([]byte, bool), stop <-chan struct{}) error {
	defer f.Close()
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	rd := bufio.NewReaderSize(f, 64*1024)
	buf := lineBuf{max: max}
	for {
		chunk, err := rd.ReadSlice('\n')
		offset += int64(len(chunk))
		buf.add(chunk)
		switch {
		case err == nil:
			buf.flush(emit)
			continue
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case !errors.Is(err, io.EOF):
			return err
		}
		// At EOF a partial line stays in buf: the daemon may be midway
		// through writing it.

		select {
		case <-stop:
			return nil
		case <-time.After(250 * time.Millisecond):
		}
		if fi, serr := f.Stat(); serr == nil && fi.Size() < offset {
			if _, serr := f.Seek(0, io.SeekStart); serr != nil {
				return serr
			}
			offset = 0
			rd.Reset(f)
			buf.reset()
		}
	}
}
