package analyzer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
)

// MaxErrorBodyBytes bounds how much of a provider's failed response is kept.
//
// One knob, because the body it captures can reach a log line, an errors.Join
// chain and eventually a ticket somebody pastes it into.
const MaxErrorBodyBytes = 4 << 10

// ProviderHTTPError is a non-2xx from a model provider.
//
// # The body is captured, and shown only at debug
//
// An LLM 4xx frequently echoes the request that caused it, and the request is
// the statement. Rendering that unconditionally would copy statement text into
// stdout — a channel audit.SinkOptions redaction does not reach, so a
// deployment running `redact_statements: true` to keep query text out of its
// audit trail would find fragments of it in the process log.
//
// So Error() renders the body only when the process is logging at debug, and
// says so when it is not. The default is an operator who can see that a
// detail exists and one config line away from reading it; turning debug on is
// then an explicit, reviewable act rather than a default nobody chose.
//
// Do NOT reach for `send: redacted` as a mitigation. Despite the name it does
// not withhold values — see sidecar.redactorFor, which appends a note naming
// the entity classes and transmits the statement unchanged. Only
// `send: refuse` keeps a statement carrying a detected entity out of the
// request in the first place.
type ProviderHTTPError struct {
	// Provider names the caller: "analyzer/vertex", not "analyzer/anthropic",
	// even though Vertex reuses the Anthropic parser. Without it a Vertex
	// user reading an anthropic-prefixed error goes looking for an anthropic
	// block in a config that has none.
	Provider string

	// StatusCode and Status are the HTTP status, separated so a caller can
	// branch on the code (401 and 429 want different responses) without
	// parsing the text.
	StatusCode int
	Status     string

	// Body is the response payload, sanitized and truncated to
	// MaxErrorBodyBytes.
	//
	// Captured only for StatusCode >= 400, which is where a provider explains
	// itself. A 3xx reaching here means the client stopped following
	// redirects, and its body is a redirect page rather than a diagnosis.
	//
	// Populated regardless of log level: the gate is on RENDERING, in
	// Error(). A caller that wants the payload programmatically reads this
	// field and takes responsibility for where it puts it.
	Body string

	// Truncated reports that Body is a prefix. Always stated in the message
	// rather than left to be inferred from the length, because a reader who
	// cannot tell a complete short body from a cut one will chase the wrong
	// half of a JSON document.
	Truncated bool

	// ContentLength is what the provider declared, or -1 when it declared
	// nothing. Only used to say how much was cut.
	ContentLength int64
}

// NewProviderHTTPError reads a failed response and consumes its body.
//
// It never returns nil, and it never fails: a body that cannot be read yields
// an error reporting the status alone, which is strictly better than the
// caller having no error to return.
func NewProviderHTTPError(provider string, resp *http.Response) *ProviderHTTPError {
	e := &ProviderHTTPError{
		Provider:      provider,
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		ContentLength: resp.ContentLength,
	}
	if e.Status == "" {
		e.Status = fmt.Sprintf("%d", resp.StatusCode)
	}

	if resp.StatusCode >= 400 {
		// One byte past the limit, so truncation is DETECTED rather than
		// inferred from a body that happens to land exactly on it.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes+1))
		if len(raw) > MaxErrorBodyBytes {
			raw = raw[:MaxErrorBodyBytes]
			e.Truncated = true
		}
		e.Body = sanitizeErrorBody(raw)
	}

	// Drain a bounded remainder so the connection can go back to the pool. A
	// body larger than this leaves the connection to be closed instead, which
	// costs a handshake and never correctness.
	_, _ = io.CopyN(io.Discard, resp.Body, MaxErrorBodyBytes)
	return e
}

// Error implements error.
//
// The body is included only at debug. Everything else — provider, status, and
// the fact that a body exists — is always present, so the message is
// actionable at any level and names the switch that reveals the rest.
func (e *ProviderHTTPError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: provider returned %s", e.Provider, e.Status)

	if e.StatusCode >= 400 && e.Body == "" {
		b.WriteString(" with an empty response body")
		return b.String()
	}
	if e.Body == "" {
		return b.String()
	}

	if !debugEnabled() {
		fmt.Fprintf(&b, " (%d-byte response body withheld; set log_level: debug to see it)",
			len(e.Body))
		return b.String()
	}

	b.WriteString(": ")
	b.WriteString(e.Body)
	switch {
	case e.Truncated && e.ContentLength > int64(MaxErrorBodyBytes):
		fmt.Fprintf(&b, " [truncated to %d of %d bytes]", MaxErrorBodyBytes, e.ContentLength)
	case e.Truncated:
		fmt.Fprintf(&b, " [truncated to %d bytes]", MaxErrorBodyBytes)
	}
	return b.String()
}

// debugEnabled reports whether this process is logging at debug.
//
// It asks the default slog logger rather than taking a config field, so there
// is ONE source of truth: sidecar.newLogger builds the handler from
// `log_level` and installs it with slog.SetDefault, and proxy.Config already
// falls back to slog.Default() for the same reason. A separate
// `verbose_errors` switch would be a second thing to set and a second thing to
// forget.
func debugEnabled() bool {
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}

// sanitizeErrorBody makes a remote string safe to put in a log line.
//
// Three things, all of them about the fact that this text is chosen by
// somebody else:
//
//   - Invalid UTF-8 is dropped, including the partial rune a byte-wise
//     truncation leaves at the end.
//   - Runs of whitespace collapse to one space. A newline in a structured log
//     line is how a remote string forges a second log record.
//   - Non-printable runes are dropped, so an escape sequence cannot repaint
//     the terminal of whoever is reading the log.
func sanitizeErrorBody(b []byte) string {
	b = bytes.ToValidUTF8(b, nil)

	var sb strings.Builder
	sb.Grow(len(b))
	pendingSpace := false
	for _, r := range string(b) {
		switch {
		case unicode.IsSpace(r):
			// Held rather than written, so trailing whitespace produces
			// nothing and a run produces one space.
			pendingSpace = true
		case unicode.IsPrint(r):
			if pendingSpace && sb.Len() > 0 {
				sb.WriteRune(' ')
			}
			pendingSpace = false
			sb.WriteRune(r)
		default:
			// Dropped, but it leaves a seam. Removing a control character
			// silently would fuse the tokens either side of it into a word
			// that was never in the body — which is how a reader ends up
			// searching a provider's docs for a string nobody sent.
			pendingSpace = true
		}
	}
	return sb.String()
}
