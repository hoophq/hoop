package analyzer

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

// MaxErrorBodyBytes bounds how much of a provider's failed response is quoted
// back in the error.
//
// One knob, because the error it builds reaches a log line, an errors.Join
// chain and eventually a ticket somebody pastes it into. Raising it makes a
// provider outage noisier by exactly this much per failed statement.
const MaxErrorBodyBytes = 4 << 10

// ProviderHTTPError is a non-2xx from a model provider.
//
// # This body reaches the relay's logs
//
// An LLM 4xx frequently echoes the request that caused it, and the request is
// the statement. So a provider's validation error can copy statement text —
// and whatever the statement contained — into stdout, which is a channel
// audit.SinkOptions redaction does not reach: a deployment running
// `redact_statements: true` keeps query text out of its audit trail and would
// now find fragments of it here.
//
// The only thing bounding that today is this function: the body is truncated
// to MaxErrorBodyBytes and stripped of anything unprintable. `send: redacted`
// does NOT help, whatever its name suggests — see sidecar.redactorFor, which
// appends a note naming the entity classes and transmits the values anyway.
//
// It is quoted anyway because the alternative was worse in practice: a wrong
// model id, a model that is not enabled, a bad region and a malformed request
// all arrive as the same bare "provider returned 404 Not Found", and the one
// sentence explaining which was discarded microseconds before it would have
// been useful. If that trade is wrong for a deployment, the fix is a config
// switch here rather than going back to a status line nobody can act on.
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

	// Body is the response body, truncated to MaxErrorBodyBytes and
	// sanitized: collapsed whitespace, no control characters. Empty when the
	// provider sent none.
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

	// One byte past the limit, so truncation is DETECTED rather than inferred
	// from a body that happens to land exactly on it.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes+1))
	if len(raw) > MaxErrorBodyBytes {
		raw = raw[:MaxErrorBodyBytes]
		e.Truncated = true
	}
	e.Body = sanitizeErrorBody(raw)

	// Drain a bounded remainder so the connection can go back to the pool. A
	// body larger than this leaves the connection to be closed instead, which
	// costs a handshake and never correctness.
	_, _ = io.CopyN(io.Discard, resp.Body, MaxErrorBodyBytes)
	return e
}

// Error implements error.
func (e *ProviderHTTPError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: provider returned %s", e.Provider, e.Status)
	if e.Body == "" {
		b.WriteString(" with an empty response body")
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
