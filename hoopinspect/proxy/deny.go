package proxy

import (
	"encoding/binary"
	"fmt"

	"github.com/hoophq/hoopinspect"
)

// ProtocolDenyWriter renders a denial in each protocol's native error frame.
//
// # Why bother
//
// Envoy's RBAC filter denies by dropping the connection. The developer sees
// "connection reset by peer", has no idea a policy exists, and files a
// ticket. A native error frame turns that into a line in their terminal:
//
//	ERROR:  destructive statements are not permitted on appdb
//
// They fix it themselves. Every hour of support this saves is the argument
// for carrying an operator-authored message all the way from the rule
// definition to the client socket, which is why Verdict.Message exists at
// all.
//
// A protocol with no shipped codec returns nil and the connection simply
// closes. That is honest: without a decoder there is no statement to explain
// a denial about.
type ProtocolDenyWriter struct{}

// Deny implements DenyWriter.
func (ProtocolDenyWriter) Deny(proto hoopinspect.Protocol, dir hoopinspect.Direction, msg string) []byte {
	if msg == "" {
		msg = "denied by policy"
	}
	switch proto {
	case hoopinspect.Postgres:
		return PostgresError(msg)
	case hoopinspect.HTTP:
		return HTTPForbidden(msg)
	}
	return nil
}

// PostgresError builds an ErrorResponse ('E') message.
//
// Wire format: each field is a one-byte type code, a NUL-terminated value,
// and the set is terminated by a zero byte.
// https://www.postgresql.org/docs/current/protocol-error-fields.html
//
// Severity is FATAL rather than ERROR because the connection is about to
// close: reporting ERROR would leave psql waiting for a ReadyForQuery that
// never arrives, and the user would see a hang instead of the message.
func PostgresError(msg string) []byte {
	var body []byte
	field := func(code byte, val string) {
		body = append(body, code)
		body = append(body, val...)
		body = append(body, 0)
	}

	field('S', "FATAL")
	field('V', "FATAL")
	// 42501 insufficient_privilege: the closest standard SQLSTATE for "a
	// policy refused this", and one clients already surface sensibly.
	field('C', "42501")
	field('M', msg)
	body = append(body, 0) // field terminator

	out := make([]byte, 0, 5+len(body))
	out = append(out, 'E')
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)+4))
	return append(out, body...)
}

// HTTPForbidden builds a 403 response.
//
// Connection: close is set because the caller is about to drop the socket;
// without it a keep-alive client waits for a second response that never
// comes.
func HTTPForbidden(msg string) []byte {
	body := msg + "\n"
	return []byte(fmt.Sprintf(
		"HTTP/1.1 403 Forbidden\r\n"+
			"Content-Type: text/plain; charset=utf-8\r\n"+
			"Content-Length: %d\r\n"+
			"X-Hoop-Denied: policy\r\n"+
			"Connection: close\r\n"+
			"\r\n%s", len(body), body))
}
