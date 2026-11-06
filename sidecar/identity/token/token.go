// Package token admits a request by a shared caller token read from a file,
// and names the caller with a configured name.
//
// # Why a shared token
//
// An http lane on a public URL has no authenticating proxy in front to
// trust (identity_header) and its callers hold no Google bearer
// (google_identity). A Claude Managed Agents cloud sandbox is the case that
// asked for this: its vault fills a request header with a secret at egress,
// so the agent never sees the value, and the only thing the sidecar can check
// is that the value is one it issued. That is all this package does.
//
// # Trust model
//
// A token names no person. Every holder of a listed token is the one caller
// the lane's configured name stands for, and the audit trail says so; a
// per-user trail needs a per-user credential, which this is not. The token
// gates the lane, then the upstream authorizes the request with its own
// credential, as it would without the sidecar.
//
// # What is kept, and where
//
// Only SHA-256 digests are held, so a heap dump of the relay yields no token.
// Every candidate is compared with every digest in constant time, so neither
// the timing of a refusal nor the line a token sits on tells a caller how
// close it came. Errors never carry a token or a candidate.
//
// # Rotation
//
// The file holds one token per line, so a new token can be served beside the
// old one while the vault that fills the header catches up, and the old line
// removed after. File re-reads the file when its size or mtime moves; a read
// that fails keeps the tokens already in force (see File.Poll).
package token

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hoophq/hoop/sidecar/session"
)

// maxFileBytes bounds what is read from a token file. A token is tens of
// bytes and a file holds a handful, so a larger file is the wrong path.
const maxFileBytes = 64 << 10

// Sentinels the resolver wraps, so a caller can tell the two refusals apart.
// The text reaches the client in its 403 body after "request refused: ".
var (
	// ErrNoToken: the header was absent, or held only whitespace. The text
	// is what the integration guides quote for a request without the token.
	ErrNoToken = errors.New("the request carried no credential")
	// ErrInvalidToken: the value is not a listed token.
	ErrInvalidToken = errors.New("the caller token is not accepted")
)

// Set is an immutable group of accepted tokens, held as digests.
type Set struct {
	digests [][sha256.Size]byte
}

// parse reads one token per line. Surrounding whitespace, blank lines and a
// leading UTF-8 byte order mark (an editor's, not part of the token) are
// ignored; a set with no token is an error, because a lane that accepts
// nothing is a misconfiguration rather than a lock.
//
// A token may hold no whitespace, comma or control character. The HTTP codec
// joins a repeated header into "a, b", so a token with ", " in it would be
// matched by two headers that each carry half of it, and a value with a
// control character cannot arrive in a header at all. Errors name the line,
// never its content.
func parse(raw []byte) (*Set, error) {
	s := &Set{}
	for i, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		tok := strings.TrimSpace(line)
		if tok == "" {
			continue
		}
		if strings.IndexFunc(tok, notTokenChar) >= 0 {
			return nil, fmt.Errorf("line %d: a token holds no whitespace, comma or control character", i+1)
		}
		s.digests = append(s.digests, sha256.Sum256([]byte(tok)))
	}
	if len(s.digests) == 0 {
		return nil, errors.New("holds no tokens")
	}
	return s, nil
}

func notTokenChar(r rune) bool {
	return r <= ' ' || r == 0x7f || r == ','
}

// Len is the number of tokens in the set, for a log line.
func (s *Set) Len() int { return len(s.digests) }

// Match reports whether candidate is one of the tokens. It digests the
// candidate and compares it with EVERY digest, without an early exit, so the
// time taken does not depend on which line matched or how much of a token
// the candidate got right.
func (s *Set) Match(candidate string) bool {
	sum := sha256.Sum256([]byte(candidate))
	ok := 0
	for i := range s.digests {
		ok |= subtle.ConstantTimeCompare(sum[:], s.digests[i][:])
	}
	return ok == 1
}

// load reads and parses the token file at path. Every error names the path
// and none carries the file's content.
func load(path string) (*Set, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err // *PathError: names the path
	}
	// Read-only file, fully read below; a close error carries nothing.
	defer func() { _ = fh.Close() }()
	raw, err := io.ReadAll(io.LimitReader(fh, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxFileBytes {
		return nil, fmt.Errorf("%s holds more than %d bytes; a token file holds a few tokens, one per line", path, maxFileBytes)
	}
	s, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %w", path, err)
	}
	return s, nil
}

// Matcher is what Resolver asks of its tokens: a Set, or a File that
// reloads one.
type Matcher interface {
	Match(candidate string) bool
}

// Resolver implements gate.RequestIdentity for a caller token: an accepted
// token resolves to one configured name, anything else is refused. Safe for
// concurrent use when its Matcher is.
type Resolver struct {
	subject string
	tokens  Matcher
}

// NewResolver returns a Resolver naming every accepted caller subject. A
// blank subject is refused: the identity would read as anonymous, and the
// trail would not say who the token stands for.
func NewResolver(subject string, tokens Matcher) (*Resolver, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("token: a caller name is required, it is the principal the audit trail records")
	}
	return &Resolver{subject: strings.TrimSpace(subject), tokens: tokens}, nil
}

// Resolve admits credential, one request's raw header value, when it is a
// listed token. The value is trimmed first, so the whitespace an
// intermediary may leave around it is not part of the token, and a value of
// whitespace alone is refused the same way as no header.
func (r *Resolver) Resolve(_ context.Context, credential string) (session.Identity, error) {
	v := strings.TrimSpace(credential)
	if v == "" {
		return session.Identity{}, ErrNoToken
	}
	if !r.tokens.Match(v) {
		return session.Identity{}, ErrInvalidToken
	}
	return session.Identity{Subject: r.subject}, nil
}
