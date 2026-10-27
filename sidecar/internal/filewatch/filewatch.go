// Package filewatch holds a value read from a file on the sidecar host, and
// reads it again when the file changes.
//
// The caller token file (identity/token) and the upstream credential file
// (upstreamcred) share it, so the two agree on when a file is read and on
// what a read that fails does: it keeps the value in force.
package filewatch

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Options names a Watched file in its log lines.
type Options[T any] struct {
	// Reloaded is the message of a read that replaced the value, Kept the
	// message of one that failed and kept it.
	Reloaded, Kept string

	// Attrs adds log attributes that describe a value, such as a count. It
	// must never return the value's content. Nil adds none.
	Attrs func(T) []any

	// Log receives both lines. Nil discards them.
	Log *slog.Logger
}

// Watched is a value read from a path and read again when the file changes.
// Get is safe for concurrent use and takes no lock; Poll and Reload are
// meant for one watching goroutine, and lock anyway.
type Watched[T any] struct {
	path string
	read func(path string) (T, error)
	opts Options[T]
	cur  atomic.Pointer[T] // never nil after Open

	mu      sync.Mutex
	size    int64
	mod     time.Time
	pending bool // the last read failed; read again without a stat change
	broken  bool // the failure was logged; say nothing until a read succeeds
}

// Open reads the file once with read. An error here is the caller's to
// report, and stops what needs the value: keeping "the old value" means
// nothing before there is one.
func Open[T any](path string, read func(path string) (T, error), opts Options[T]) (*Watched[T], error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	w := &Watched[T]{path: path, read: read, opts: opts}
	// Stat before the read: an edit landing between the two leaves a stat
	// older than the content, which the next Poll reads again, rather than
	// a newer stat that would hide the edit.
	st, err := os.Stat(path)
	if err != nil {
		return nil, err // *PathError: names the path
	}
	w.size, w.mod = st.Size(), st.ModTime()
	v, err := read(path)
	if err != nil {
		return nil, err
	}
	w.cur.Store(&v)
	return w, nil
}

// Get returns the value in force.
func (w *Watched[T]) Get() T { return *w.cur.Load() }

// Poll reads the file again when its size or mtime moved since the last
// read, or when the last read failed. It is the config file's rule (daemon's
// reloader), for the same reason: a Kubernetes Secret volume swaps the file
// by an atomic symlink change that a stat sees and an inotify watch on the
// old inode does not.
//
// A file that is missing or that read refuses KEEPS the value in force, and
// is logged once until a read succeeds. The read may have caught an editor
// mid-write, and refusing every request over it would turn a typo into an
// outage.
func (w *Watched[T]) Poll() { w.refresh(false) }

// Reload reads the file whether or not its stat moved, for SIGHUP: a
// rewrite that keeps the size within the filesystem's mtime granularity is
// invisible to Poll.
func (w *Watched[T]) Reload() { w.refresh(true) }

func (w *Watched[T]) refresh(force bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, err := os.Stat(w.path)
	if err != nil {
		w.failed(err)
		return
	}
	if !force && !w.pending && st.Size() == w.size && st.ModTime().Equal(w.mod) {
		return
	}
	w.size, w.mod = st.Size(), st.ModTime()
	v, err := w.read(w.path)
	if err != nil {
		w.failed(err)
		return
	}
	w.cur.Store(&v)
	w.pending, w.broken = false, false
	w.opts.Log.Info(w.opts.Reloaded, append([]any{"path", w.path}, w.attrs(v)...)...)
}

// failed records a read that kept the value in force. Called with w.mu held.
func (w *Watched[T]) failed(err error) {
	w.pending = true
	if w.broken {
		return
	}
	w.broken = true
	w.opts.Log.Warn(w.opts.Kept, append([]any{"path", w.path, "error", err}, w.attrs(w.Get())...)...)
}

func (w *Watched[T]) attrs(v T) []any {
	if w.opts.Attrs == nil {
		return nil
	}
	return w.opts.Attrs(v)
}
