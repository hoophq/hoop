package token

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// File is a token Set read from a path and re-read when the file changes.
// Match is safe for concurrent use and takes no lock; Poll and Reload are
// meant for one watching goroutine, and lock anyway.
type File struct {
	path string
	log  *slog.Logger
	cur  atomic.Pointer[Set] // never nil after Open

	mu      sync.Mutex
	size    int64
	mod     time.Time
	pending bool // the last read failed; read again without a stat change
	broken  bool // the failure was logged; say nothing until a read succeeds
}

// Open reads the file once. An error here stops the lane from starting: a
// lane that cannot know its tokens must not serve, and keeping "the old
// tokens" means nothing before there are any.
func Open(path string, log *slog.Logger) (*File, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	f := &File{path: path, log: log}
	// Stat before the read: an edit landing between the two leaves a stat
	// older than the content, which the next Poll reads again, rather than
	// a newer stat that would hide the edit.
	if st, err := os.Stat(path); err == nil {
		f.size, f.mod = st.Size(), st.ModTime()
	}
	s, err := load(path)
	if err != nil {
		return nil, err
	}
	f.cur.Store(s)
	return f, nil
}

// Match reports whether candidate is a token in force; see Set.Match.
func (f *File) Match(candidate string) bool { return f.cur.Load().Match(candidate) }

// Poll re-reads the file when its size or mtime moved since the last read,
// or when the last read failed. It is the config file's rule (daemon's
// reloader), for the same reason: a Kubernetes Secret volume swaps the file
// by an atomic symlink change that a stat sees and an inotify watch on the
// old inode does not.
//
// A file that is missing, empty or unreadable KEEPS the tokens in force, and
// is logged once until a read succeeds. The read may have caught an editor
// mid-write, and refusing every caller over it would turn a typo into an
// outage. Revoking a token is writing a file without it.
func (f *File) Poll() { f.refresh(false) }

// Reload re-reads the file whether or not its stat moved, for SIGHUP: a
// rewrite that keeps the size within the filesystem's mtime granularity is
// invisible to Poll.
func (f *File) Reload() { f.refresh(true) }

func (f *File) refresh(force bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, err := os.Stat(f.path)
	if err != nil {
		f.failed(err)
		return
	}
	if !force && !f.pending && st.Size() == f.size && st.ModTime().Equal(f.mod) {
		return
	}
	f.size, f.mod = st.Size(), st.ModTime()
	s, err := load(f.path)
	if err != nil {
		f.failed(err)
		return
	}
	f.cur.Store(s)
	f.pending, f.broken = false, false
	f.log.Info("caller token file reloaded", "path", f.path, "tokens", s.Len())
}

// failed records a read that kept the tokens in force. Called with f.mu
// held.
func (f *File) failed(err error) {
	f.pending = true
	if f.broken {
		return
	}
	f.broken = true
	f.log.Warn("the caller token file cannot be read; keeping the tokens in force",
		"path", f.path, "error", err, "tokens", f.cur.Load().Len())
}
