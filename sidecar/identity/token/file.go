package token

import (
	"log/slog"

	"github.com/hoophq/hoop/sidecar/internal/filewatch"
)

// File is a token Set read from a path and re-read when the file changes.
// Match is safe for concurrent use and takes no lock; Poll and Reload are
// meant for one watching goroutine, and lock anyway.
//
// A file that is missing, empty or unreadable on a re-read KEEPS the tokens
// in force (see filewatch.Watched.Poll). Revoking a token is writing a file
// without it.
type File struct {
	*filewatch.Watched[*Set]
}

// Open reads the file once. An error here stops the lane from starting: a
// lane that cannot know its tokens must not serve, and keeping "the old
// tokens" means nothing before there are any.
func Open(path string, log *slog.Logger) (*File, error) {
	w, err := filewatch.Open(path, load, filewatch.Options[*Set]{
		Reloaded: "caller token file reloaded",
		Kept:     "the caller token file cannot be read; keeping the tokens in force",
		Attrs:    func(s *Set) []any { return []any{"tokens", s.Len()} },
		Log:      log,
	})
	if err != nil {
		return nil, err
	}
	return &File{w}, nil
}

// Match reports whether candidate is a token in force; see Set.Match.
func (f *File) Match(candidate string) bool { return f.Get().Match(candidate) }
