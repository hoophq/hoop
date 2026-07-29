// Package postgres decodes the PostgreSQL v3 frontend/backend protocol far
// enough to recover the SQL a client sent.
//
// Wire format reference:
// https://www.postgresql.org/docs/current/protocol-message-formats.html
//
// A frontend message after the startup phase is:
//
//	byte1   message type tag
//	int32   length, INCLUDING these 4 bytes but NOT the tag
//	...     payload
//
// Two message types carry SQL:
//
//	'Q' Query  — simple query: a NUL-terminated string, possibly several
//	             statements separated by semicolons.
//	'P' Parse  — extended query: prepared-statement name (NUL-terminated,
//	             empty for the unnamed statement), then the query string
//	             (NUL-terminated), then parameter type OIDs.
//
// Everything else is skipped by length. The startup packet (which has no type
// tag) and the SSLRequest sentinel are recognized so the decoder stays in sync
// from the first byte of the connection.
package postgres

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/hoophq/hoopinspect"
)

func init() { hoopinspect.Register(func() hoopinspect.Codec { return &Codec{} }) }

// Codec implements hoopinspect.Codec for PostgreSQL.
//
// It is stateful on the response side: a RowDescription describes every
// DataRow that follows it, and those messages routinely land in different
// TCP reads. One Codec per connection — the registry hands out a factory for
// exactly this reason.
type Codec struct {
	// rowDesc is the column layout of the result set currently streaming,
	// nil between result sets.
	rowDesc *rowDescription

	// rowCount and truncated accumulate across reads until a terminator.
	rowCount  int
	truncated bool

	// Rewrite state. Separate from the decode state above because the two
	// run on independent copies of the stream: Decode inspects, Rewrite
	// rebuilds, and a caller may use either alone.
	//
	// held buffers complete DataRow messages awaiting masking; pending holds
	// a trailing partial message until its remainder arrives; maskCols is the
	// current column layout the masker is shown.
	held     []byte
	pending  []byte
	maskCols []string
}

func (*Codec) Protocol() hoopinspect.Protocol { return hoopinspect.Postgres }

// Message type tags we care about. The rest are skipped generically.
const (
	tagQuery = 'Q'
	tagParse = 'P'
)

// sslRequestCode and cancelRequestCode occupy the "protocol version" field of
// an untagged startup-shaped packet. Recognizing them keeps the decoder from
// treating a handshake as a malformed message.
const (
	sslRequestCode    uint32 = 80877103
	cancelRequestCode uint32 = 80877102
	gssEncRequestCode uint32 = 80877104
)

// maxMessageLen rejects a length field that could only come from garbage or a
// hostile peer. Postgres itself caps a message at 1 GB; we refuse anything
// above 64 MiB because a statement that large is not something a policy engine
// should be asked to evaluate inline.
const maxMessageLen = 64 << 20

// ErrMalformed means the byte stream is not valid Postgres v3. The connection
// cannot be resynchronized.
var ErrMalformed = errors.New("hoopinspect/postgres: malformed message")

// Decode implements hoopinspect.Codec.
//
// Metadata keys set on returned statements:
//
//	"pg.message"   — "Query", "Parse" or "ErrorResponse"
//	"pg.statement" — prepared-statement name, only for a named Parse
//
// Server → client messages yield one statement per completed result set,
// carrying Statement.Result: the column names the server described and the
// row count. See response.go.
func (c *Codec) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	if dir == hoopinspect.FromServer {
		return c.decodeResponse(data)
	}

	var stmts []hoopinspect.Statement
	pos := 0

	for pos < len(data) {
		// Untagged handshake packets: int32 length, int32 code.
		if n, handled := skipHandshake(data[pos:]); handled {
			if n == 0 {
				return stmts, pos, nil // incomplete, wait for more
			}
			pos += n
			continue
		}

		// Need tag + length before anything can be decided.
		if len(data)-pos < 5 {
			return stmts, pos, nil
		}

		tag := data[pos]
		msgLen := binary.BigEndian.Uint32(data[pos+1 : pos+5])

		// Length counts itself (4) but not the tag, so the smallest legal
		// value is 4. Anything below that cannot be resynchronized.
		if msgLen < 4 || msgLen > maxMessageLen {
			return stmts, pos, ErrMalformed
		}

		total := 1 + int(msgLen) // tag + declared length
		if len(data)-pos < total {
			return stmts, pos, nil // partial message, retain it
		}

		payload := data[pos+5 : pos+total]

		switch tag {
		case tagQuery:
			for _, s := range splitSimpleQuery(cstring(payload)) {
				stmts = append(stmts, newStatement(s, "Query", ""))
			}
		case tagParse:
			name, query, ok := parseMessage(payload)
			if ok && strings.TrimSpace(query) != "" {
				stmts = append(stmts, newStatement(query, "Parse", name))
			}
		}

		pos += total
	}

	return stmts, pos, nil
}

// skipHandshake recognizes the untagged packets that precede normal message
// flow: StartupMessage, SSLRequest, GSSENCRequest and CancelRequest. It
// returns the byte count to skip and whether the packet was one of these.
//
// A returned (0, true) means "this is a handshake packet but it is not fully
// buffered yet" — the caller must wait for more bytes.
//
// Disambiguation: a real message tag is a printable ASCII letter, so a first
// byte of 0x00 can only be the high byte of a startup packet's int32 length
// (any startup packet is far smaller than 16 MiB).
func skipHandshake(data []byte) (int, bool) {
	if len(data) == 0 || data[0] != 0x00 {
		return 0, false
	}
	if len(data) < 8 {
		return 0, true // definitely a handshake, not yet complete
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if length < 8 || length > maxMessageLen {
		return 0, false // not a shape we recognize; let the caller error
	}
	if len(data) < int(length) {
		return 0, true
	}
	code := binary.BigEndian.Uint32(data[4:8])
	switch code {
	case sslRequestCode, gssEncRequestCode, cancelRequestCode:
		return int(length), true
	}
	// Otherwise it is a StartupMessage: high 16 bits are the major protocol
	// version, which is 3 for every server since 7.4.
	if code>>16 == 3 {
		return int(length), true
	}
	return 0, false
}

// parseMessage splits a Parse payload into the prepared-statement name and the
// query text. Both are NUL-terminated; the name is empty for the unnamed
// statement. Returns ok=false when the payload is truncated.
func parseMessage(payload []byte) (name, query string, ok bool) {
	i := indexNUL(payload)
	if i < 0 {
		return "", "", false
	}
	name = string(payload[:i])

	rest := payload[i+1:]
	j := indexNUL(rest)
	if j < 0 {
		return "", "", false
	}
	return name, string(rest[:j]), true
}

// cstring returns the bytes up to the first NUL, or the whole slice when
// unterminated (a server would reject that, but we still want the text).
func cstring(b []byte) string {
	if i := indexNUL(b); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func indexNUL(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

// splitSimpleQuery breaks a simple-query payload on top-level semicolons.
//
// This matters for policy: `SELECT 1; DROP TABLE users` is ONE 'Q' message,
// and a decoder that classified it by its leading verb alone would report a
// harmless select and wave the DROP through. Splitting means every statement
// gets its own verdict.
//
// Semicolons inside string literals, quoted identifiers and dollar-quoted
// bodies are not separators and are skipped.
func splitSimpleQuery(q string) []string {
	var out []string
	start := 0

	for i := 0; i < len(q); {
		switch q[i] {
		case '\'':
			i = skipQuoted(q, i, '\'')
		case '"':
			i = skipQuoted(q, i, '"')
		case '$':
			// Dollar quoting: $tag$ ... $tag$ (tag may be empty).
			if end, tag, ok := dollarTag(q, i); ok {
				if close := strings.Index(q[end:], tag); close >= 0 {
					i = end + close + len(tag)
				} else {
					i = len(q) // unterminated
				}
			} else {
				i++
			}
		case '-':
			if i+1 < len(q) && q[i+1] == '-' {
				for i < len(q) && q[i] != '\n' {
					i++
				}
			} else {
				i++
			}
		case '/':
			if i+1 < len(q) && q[i+1] == '*' {
				i += 2
				for i+1 < len(q) && !(q[i] == '*' && q[i+1] == '/') {
					i++
				}
				i += 2
			} else {
				i++
			}
		case ';':
			if s := strings.TrimSpace(q[start:i]); s != "" {
				out = append(out, s)
			}
			i++
			start = i
		default:
			i++
		}
	}
	if s := strings.TrimSpace(q[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// skipQuoted advances past a quoted run starting at i, handling the doubled
// quote escape. Returns the index just past the closing quote.
func skipQuoted(q string, i int, quote byte) int {
	i++ // opening quote
	for i < len(q) {
		if q[i] == quote {
			if i+1 < len(q) && q[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// dollarTag recognizes a dollar-quote opener at i and returns the index just
// past it plus the tag to search for as the closer.
func dollarTag(q string, i int) (end int, tag string, ok bool) {
	j := i + 1
	for j < len(q) {
		c := q[j]
		if c == '$' {
			return j + 1, q[i : j+1], true
		}
		isTagChar := c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9')
		if !isTagChar {
			return 0, "", false
		}
		j++
	}
	return 0, "", false
}

func newStatement(text, msgType, stmtName string) hoopinspect.Statement {
	op, tables := hoopinspect.ClassifySQL(text)
	md := map[string]string{"pg.message": msgType}
	if stmtName != "" {
		md["pg.statement"] = stmtName
	}
	return hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      text,
		Operation: op,
		Tables:    tables,
		Metadata:  md,
	}
}
