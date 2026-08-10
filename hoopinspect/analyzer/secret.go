package analyzer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Secret holds credential material that must never be printed.
//
// The type exists because the leak paths are not the ones anyone remembers to
// guard. A plain string in a config struct escapes through %v on a struct
// dump, through json.Marshal on a debug endpoint, through slog when someone
// logs the config, and through a panic trace. Secret closes all four at once
// by refusing to render, so a future field added next to it cannot leak by
// omission of a tag.
//
// The zero Secret is valid and empty, which is the legitimate state for a
// provider resolving ambient credentials.
type Secret struct {
	b []byte
}

// NewSecret wraps b. The caller must not retain or mutate b afterwards.
func NewSecret(b []byte) Secret { return Secret{b: b} }

// Bytes returns the raw material. Every call site is a place a credential can
// escape, so there should be exactly one per provider: the line that builds
// the HTTP request.
func (s Secret) Bytes() []byte { return s.b }

// String renders a placeholder. It satisfies fmt.Stringer, so %v and %s on a
// Secret or on any struct containing one print this instead of the material.
func (s Secret) String() string {
	if len(s.b) == 0 {
		return "<unset>"
	}
	return "[REDACTED]"
}

// GoString covers %#v, which ignores Stringer and would otherwise dump the
// byte slice.
func (s Secret) GoString() string { return s.String() }

// MarshalJSON covers encoding/json. A Secret reachable from anything an admin
// endpoint serializes emits the placeholder rather than the material.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// LogValue covers log/slog structured logging.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// IsZero reports whether the secret holds nothing.
func (s Secret) IsZero() bool { return len(s.b) == 0 }

// ErrCredentialPerms reports a credential file readable beyond its owner.
var ErrCredentialPerms = errors.New("hoopinspect/analyzer: credential file is readable by group or other")

// ReadSecretFile reads a credential from disk, refusing a file that anyone
// but its owner can read.
//
// The refusal is the same one ssh applies to a private key, and for the same
// reason: a 0644 credential on a shared host is already disclosed, and
// failing at startup is the only moment an operator will act on it. Reporting
// the mode in the error matters because the usual cause is a file mode carried
// in from a git checkout or a ConfigMap default, and the operator needs to see
// the number to believe it.
func ReadSecretFile(path string) (Secret, error) {
	if path == "" {
		return Secret{}, errors.New("hoopinspect/analyzer: empty credential path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Secret{}, fmt.Errorf("hoopinspect/analyzer: credential file: %w", err)
	}
	if info.IsDir() {
		return Secret{}, fmt.Errorf("hoopinspect/analyzer: credential path %s is a directory", path)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return Secret{}, fmt.Errorf("%w: %s is %04o, want 0600 or stricter",
			ErrCredentialPerms, path, mode)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Secret{}, fmt.Errorf("hoopinspect/analyzer: credential file: %w", err)
	}
	// A credential pasted into a file almost always carries a trailing
	// newline, and an API key with \n appended fails authentication with a
	// message that names neither the newline nor the file.
	return NewSecret([]byte(strings.TrimSpace(string(b)))), nil
}
