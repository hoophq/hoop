package ipc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// ControlToken is a cryptographically random secret the daemon writes
// to disk so the local UI/CLI can authenticate to the control plane
// without an interactive credential exchange.
//
// The token rotates on every daemon startup (a fresh value is generated
// and persisted) so a leaked token cannot grant indefinite access. The
// trust boundary is the local filesystem ACL: any user who can read the
// token file can drive the daemon. The defaults are:
//
//   - Unix: file written with mode 0640 owned by root:hsh. Only members
//     of group hsh — typically just the installing user — can read it.
//   - Windows: file written with a DACL granting read to the local
//     Users group, set in socket_windows.go's writer wrapper.
//
// Hex-encoded 32-byte random produces a 64-char token. That's overkill
// for a local-only secret but cheap, and saves us from worrying about
// alphabet ambiguity on copy-paste during debugging.
type ControlToken string

// String redacts the token when formatted with %s or %v so it does not
// leak into accidental log lines. Use Raw() when actually transmitting.
func (t ControlToken) String() string { return "<redacted>" }

// Raw returns the underlying token string. Callers MUST treat this as
// a secret: never log it, never embed it in error messages.
func (t ControlToken) Raw() string { return string(t) }

// IsZero reports whether the token is empty (uninitialised).
func (t ControlToken) IsZero() bool { return string(t) == "" }

// constantTimeEquals compares two tokens without short-circuiting on
// the first mismatched byte. Critical for any equality check that runs
// on attacker-controlled input.
func (t ControlToken) constantTimeEquals(other string) bool {
	a := []byte(string(t))
	b := []byte(other)
	if len(a) != len(b) {
		// Still run the compare on equal-length copies so the timing
		// signal does not leak the length itself.
		_ = subtle.ConstantTimeCompare(a, a)
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// GenerateControlToken produces a fresh random control token.
//
// It returns an error only if the operating system's CSPRNG is broken,
// which on Linux/macOS/Windows essentially means the process is in an
// unrecoverable state — callers should treat the error as fatal.
func GenerateControlToken() (ControlToken, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("ipc: generate control token: %w", err)
	}
	return ControlToken(hex.EncodeToString(buf[:])), nil
}

// TokenStore is the abstraction the daemon uses to persist and rotate
// the control token. Two real implementations are shipped:
//
//   - fileTokenStore (see NewFileTokenStore) writes the token to a file
//     on disk. The daemon uses this in production.
//   - MemoryTokenStore keeps the token in memory only. Tests use it.
//
// The interface is intentionally minimal: read the current token, swap
// in a new one. Concurrent reads via the validating middleware are
// safe; rotation is expected to happen at most once per daemon
// lifetime.
type TokenStore interface {
	// Current returns the token presently considered valid. It must
	// return a non-empty token after Rotate has been called at least
	// once; until then it returns the zero ControlToken.
	Current() ControlToken

	// Rotate generates a new random token, atomically replaces the
	// stored value, and returns the new token. Subsequent calls to
	// Current return the new value.
	Rotate() (ControlToken, error)
}

// MemoryTokenStore is a TokenStore that keeps the token in memory only.
// It exists so tests can drive the auth middleware without touching the
// filesystem. The zero value is a valid (empty) store; call Rotate
// before serving requests.
//
// All methods are safe for concurrent use.
type MemoryTokenStore struct {
	tok atomic.Pointer[ControlToken]
}

// Current returns the in-memory token, or zero if Rotate has never run.
func (m *MemoryTokenStore) Current() ControlToken {
	if p := m.tok.Load(); p != nil {
		return *p
	}
	return ""
}

// Rotate generates a new token and replaces the in-memory value.
func (m *MemoryTokenStore) Rotate() (ControlToken, error) {
	t, err := GenerateControlToken()
	if err != nil {
		return "", err
	}
	m.tok.Store(&t)
	return t, nil
}

// fileTokenStore is the production TokenStore: the token lives in a
// file the local UI can read, and is rewritten on rotation. We never
// keep the on-disk token around in memory beyond what Current returns;
// callers that need to authenticate outbound requests should pull it
// fresh each time.
type fileTokenStore struct {
	path string
	// gid is the group the token file is chowned to (-1 = leave at the
	// process's primary group). Resolved once from FileTokenOptions.GroupName
	// at construction so Rotate doesn't re-do the lookup on every restart.
	gid int
	tok atomic.Pointer[ControlToken]
}

// FileTokenOptions tunes how NewFileTokenStore writes the token file.
// The defaults are correct for the production deployment; tests
// override Mode and DirMode to avoid root-required permissions.
type FileTokenOptions struct {
	// Mode is the unix file mode applied to the token file. Defaults to
	// 0640 — only owner write and group read. On Windows this is best-
	// effort: Go's os.Chmod only adjusts the user-read bit.
	Mode os.FileMode

	// DirMode is the mode applied to the containing directory if it has
	// to be created. Defaults to 0750.
	DirMode os.FileMode

	// GroupName, when set, is the OS group the token file is chowned to
	// so members of that group can read it (the IPC group, typically
	// "hsh"). Without this the file stays owned by the daemon's primary
	// group (e.g. root:daemon on macOS, root:root on Linux) and the
	// 0640 mode's group-read bit is useless because no non-root user is
	// in that group. Empty leaves the group at the process default —
	// fine for tests and dev runs that don't use a group ACL. Ignored on
	// Windows (which uses a DACL instead).
	GroupName string
}

// NewFileTokenStore constructs a TokenStore that persists the control
// token at the given path. It does NOT generate a token; the caller
// must call Rotate (typically at daemon startup).
//
// The parent directory is created if missing with DirMode permissions.
// The file itself is written with Mode (default 0640) so the daemon
// (running as root) is the writer and the local user (member of group
// hsh) is the reader.
func NewFileTokenStore(path string, opts FileTokenOptions) (TokenStore, error) {
	if path == "" {
		return nil, errors.New("ipc: NewFileTokenStore: path is required")
	}
	if opts.Mode == 0 {
		opts.Mode = 0o640
	}
	if opts.DirMode == 0 {
		opts.DirMode = 0o750
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, opts.DirMode); err != nil {
		return nil, fmt.Errorf("ipc: ensure token dir %q: %w", dir, err)
	}

	// Resolve the group once. An unknown group is a configuration error
	// worth surfacing at construction rather than silently leaving the
	// token unreadable by the intended audience.
	gid := -1
	if opts.GroupName != "" {
		resolved, err := lookupGID(opts.GroupName)
		if err != nil {
			return nil, fmt.Errorf("ipc: resolve token group %q: %w", opts.GroupName, err)
		}
		gid = resolved
	}

	s := &fileTokenStore{path: path, gid: gid}
	// If a token file from a previous daemon lifetime still exists, do
	// NOT reuse it: we want every restart to rotate. The caller is
	// expected to Rotate before serving the first request. We still
	// truncate any leftover here so a crashed daemon never leaves a
	// stale token readable across reboots.
	if err := os.WriteFile(path, nil, opts.Mode); err != nil {
		return nil, fmt.Errorf("ipc: truncate token file %q: %w", path, err)
	}
	// Chown the (empty) file now so its group is correct even before the
	// first Rotate; Rotate re-applies it on the renamed temp file too.
	if err := chownToGroup(path, gid); err != nil {
		return nil, fmt.Errorf("ipc: chown token file %q: %w", path, err)
	}
	return s, nil
}

// Current returns the most recently rotated token.
func (s *fileTokenStore) Current() ControlToken {
	if p := s.tok.Load(); p != nil {
		return *p
	}
	return ""
}

// Rotate generates a new token, writes it atomically to disk, and
// replaces the in-memory copy. The atomicity guarantee is "write to
// temp + rename": readers that catch us mid-rotation see either the
// previous content or the new content, never a truncated file.
func (s *fileTokenStore) Rotate() (ControlToken, error) {
	t, err := GenerateControlToken()
	if err != nil {
		return "", err
	}
	// Stat the destination to preserve its mode across rotations.
	mode := os.FileMode(0o640)
	if st, statErr := os.Stat(s.path); statErr == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("ipc: temp token file: %w", err)
	}
	if _, err := tmp.WriteString(t.Raw() + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("ipc: write temp token: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("ipc: chmod temp token: %w", err)
	}
	// Apply the group ownership to the temp file *before* the rename so
	// the token is never visible at its final path with the wrong group
	// (which would briefly make it unreadable by the hsh group, racing a
	// client poll).
	if err := chownToGroup(tmp.Name(), s.gid); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("ipc: chown temp token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("ipc: close temp token: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("ipc: rename token file: %w", err)
	}
	s.tok.Store(&t)
	return t, nil
}

// authMiddleware enforces the bearer-token check on every request.
//
// Behaviour:
//   - Missing or malformed Authorization header → 401 with code
//     "unauthorized" and a stable error message.
//   - Token presented but doesn't match the current value → same 401.
//   - Comparison runs in constant time so an attacker cannot probe the
//     token byte-by-byte via timing.
//
// The middleware never logs the token, even on failure. It only logs
// the presence/absence of the header.
func authMiddleware(store TokenStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header", "unauthorized")
			return
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(raw, prefix) {
			writeError(w, http.StatusUnauthorized, "Authorization header must be a Bearer token", "unauthorized")
			return
		}
		provided := strings.TrimSpace(raw[len(prefix):])
		current := store.Current()
		if current.IsZero() || !current.constantTimeEquals(provided) {
			writeError(w, http.StatusUnauthorized, "invalid control token", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
