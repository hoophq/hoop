// Package upstreamcred holds the credential an http lane adds to every
// request it forwards, read from a file on the sidecar host.
//
// # Why
//
// An agent that calls an API through the sidecar would otherwise hold the
// API's token itself, in an environment variable a prompt can print. On
// AgentCore a token in the environment is returned by GetHarness and lands
// in CloudWatch when the agent echoes it (ENG-880). With this block the
// agent holds only the caller token, which opens the lane and nothing else,
// and the token the upstream reads never enters its sandbox.
//
// # What it guarantees
//
// The header is one that cannot change how a request is framed or routed
// (Spec.Check), and the value holds no control character, so it cannot end
// its line and start another. The value never renders: a Credential prints,
// marshals and logs as [REDACTED], and every error names the file, never
// its content. Keeping the header out of the statement is the lane's job:
// the http codec captures only the headers http.headers lists, and daemon
// refuses a list that names this one.
package upstreamcred

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/hoophq/hoop/sidecar/internal/filewatch"
)

// MinTokenBytes is the shortest token accepted. The http lane's placement
// guard does not look for a value shorter than this anywhere else in a
// request (codec/http minSecretBytes), so a shorter token could be echoed
// into a URL or a body unseen. A test there pins the two.
const MinTokenBytes = 16

// maxTokenBytes bounds what is read. An API token is tens or hundreds of
// bytes; a larger file is the wrong path, and an upstream refuses a header
// line past a few kilobytes anyway.
const maxTokenBytes = 8 << 10

// Spec is what a lane configures: the header, an optional auth scheme, and
// the file holding the token.
type Spec struct {
	Header string
	Scheme string
	File   string
}

// reserved are headers a credential may not be written to. Each changes how
// the request is framed (content-length, transfer-encoding, te, trailer,
// expect), how the connection is handled (connection, keep-alive, upgrade,
// proxy-connection), where it is routed (host, forwarded), or is written by
// the sidecar itself (via, and the x-forwarded fields an origin lane sets).
// A value placed there by configuration would be a smuggling primitive, not
// a credential. set-cookie is a response header.
var reserved = []string{
	"host", "connection", "keep-alive", "upgrade", "proxy-connection",
	"content-length", "transfer-encoding", "te", "trailer", "expect",
	"via", "forwarded", "set-cookie",
}

// reservedPrefixes are header families refused for the same reason: proxy-*
// is read and removed by an intermediary, and x-forwarded-* is the
// sidecar's, or a client's claim about where the request came from.
var reservedPrefixes = []string{"proxy-", "x-forwarded-"}

// Check reports what is wrong with s without reading its file, so a
// configuration can be checked where the file does not exist (the control
// plane, -validate on another host). Messages name the field, never a value
// read from the file.
func (s Spec) Check() []string {
	var problems []string
	h := strings.ToLower(strings.TrimSpace(s.Header))
	switch {
	case h == "":
		problems = append(problems, "header is required: it names the header the upstream reads the token from")
	case !IsToken(h):
		problems = append(problems, fmt.Sprintf("header %q is not a header name", s.Header))
	case isReserved(h):
		problems = append(problems, fmt.Sprintf(
			"header %q frames or routes the request, or the sidecar writes it; a credential cannot go there", s.Header))
	}
	if sc := strings.TrimSpace(s.Scheme); sc != "" && !IsToken(sc) {
		problems = append(problems, fmt.Sprintf("scheme %q is not an auth scheme: one word, such as Bearer", s.Scheme))
	}
	if strings.TrimSpace(s.File) == "" {
		problems = append(problems, "file is required: it holds the token")
	}
	return problems
}

func isReserved(h string) bool {
	for _, r := range reserved {
		if h == r {
			return true
		}
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	return false
}

// IsToken reports whether s is an RFC 9110 token: one or more tchar. A field
// name and an auth scheme are both tokens.
func IsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// Credential is one lane's upstream credential: a fixed header and scheme,
// and a token read from a file and read again when the file changes.
// Methods are safe for concurrent use.
type Credential struct {
	header string // as configured, trimmed
	scheme string // "" sends the token alone
	file   *filewatch.Watched[string]
}

// Open checks s and reads its file once. An error stops the lane: a lane
// that promises its upstream a credential must not forward without one.
// Every error names the file and none carries its content.
//
// Poll and Reload read the file again; a read that fails keeps the token in
// force, as a caller token file does.
func Open(s Spec, log *slog.Logger) (*Credential, error) {
	if problems := s.Check(); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	f, err := filewatch.Open(strings.TrimSpace(s.File), load, filewatch.Options[string]{
		Reloaded: "upstream credential file reloaded",
		Kept:     "the upstream credential file cannot be read; keeping the credential in force",
		Log:      log,
	})
	if err != nil {
		// Named like Check's problems, so the caller prefixes both the same.
		return nil, fmt.Errorf("file: %w", err)
	}
	return &Credential{header: strings.TrimSpace(s.Header), scheme: strings.TrimSpace(s.Scheme), file: f}, nil
}

// Header is the header the credential goes in, as configured.
func (c *Credential) Header() string { return c.header }

// Current returns the header value to send and the token inside it, from
// one read of the token in force, so a rotation between the two cannot pair
// a value with another token.
func (c *Credential) Current() (value, token string) {
	token = c.file.Get()
	if c.scheme == "" {
		return token, token
	}
	return c.scheme + " " + token, token
}

// Poll reads the file again when it changed; see filewatch.Watched.Poll.
func (c *Credential) Poll() { c.file.Poll() }

// Reload reads the file again unconditionally, for SIGHUP.
func (c *Credential) Reload() { c.file.Reload() }

// redacted is what a Credential renders as anywhere it is formatted.
const redacted = "[REDACTED]"

// String, GoString, LogValue and MarshalJSON render a Credential as
// [REDACTED], so a %v, a %#v, a slog attribute or a JSON dump of a struct
// that holds one shows no token. The token sits behind a pointer that fmt
// does not follow anyway; these make it independent of that.
func (c *Credential) String() string               { return redacted }
func (c *Credential) GoString() string             { return redacted }
func (c *Credential) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (c *Credential) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// load reads the token file at path.
//
// The file must be readable by its owner alone, as analyzer.ReadSecretFile
// and an ssh private key require: a 0644 token on a shared host is already
// disclosed, and startup is the moment an operator acts on it. os.Stat
// follows a symlink, so a Kubernetes Secret volume, whose keys are links,
// is judged by the file it projects.
//
// Surrounding whitespace and a leading UTF-8 byte order mark are an editor's
// and are dropped: a pasted token almost always ends in a newline. What is
// left must be printable ASCII. A control character, CR and LF above all,
// would end the header line and begin another the upstream reads as the
// sidecar's; a non-ASCII byte is obs-text, which upstreams read differently.
// Errors name the path and the rule, never the content.
func load(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err // *PathError: names the path
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		return "", fmt.Errorf("%s is %04o: a credential file must be readable by its owner alone, 0600 or stricter", path, mode)
	}
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	// Read-only file, fully read below; a close error carries nothing.
	defer func() { _ = fh.Close() }()
	raw, err := io.ReadAll(io.LimitReader(fh, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxTokenBytes {
		return "", fmt.Errorf("%s holds more than %d bytes; a credential file holds one token", path, maxTokenBytes)
	}
	tok := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	switch {
	case tok == "":
		return "", fmt.Errorf("%s holds no token", path)
	case strings.IndexFunc(tok, notPrintable) >= 0:
		return "", fmt.Errorf("%s holds a control or non-ASCII character; a token is printable ASCII on one line", path)
	case len(tok) < MinTokenBytes:
		return "", fmt.Errorf("%s holds a token shorter than %d bytes", path, MinTokenBytes)
	}
	return tok, nil
}

func notPrintable(r rune) bool { return r < ' ' || r > '~' }
