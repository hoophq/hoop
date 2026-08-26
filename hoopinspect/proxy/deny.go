package proxy

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"github.com/hoophq/hoopinspect"
)

// ProtocolDenyWriter renders a denial in each protocol's native error frame.
//
// # Rationale
//
// Envoy's RBAC filter denies by dropping the connection. The developer sees
// "connection reset by peer", has no idea a policy exists, and files a
// ticket. A native error frame turns that into a line in their terminal:
//
//	ERROR:  destructive statements are not permitted on appdb
//
// They fix it themselves. The support hours this saves pay for carrying an
// operator-authored message from the rule definition all the way to the
// client socket, which is why Verdict.Message exists.
//
// A protocol with no shipped codec returns nil and the connection closes.
// Without a decoder there is no statement to explain a denial about.
type ProtocolDenyWriter struct{}

// Deny implements DenyWriter.
func (ProtocolDenyWriter) Deny(proto hoopinspect.Protocol, dir hoopinspect.Direction, msg string) []byte {
	if msg == "" {
		msg = "denied by policy"
	}
	switch proto {
	case hoopinspect.Postgres:
		return PostgresError(msg)
	case hoopinspect.MSSQL:
		return MSSQLError(msg)
	case hoopinspect.HTTP:
		return HTTPForbidden(msg)
	}
	return nil
}

// TDS token-stream constants for a synthesized server error.
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/9805e9fa-1f8b-4cf8-8f78-8d2602228635
const (
	tdsTokenError byte = 0xAA
	tdsTokenDone  byte = 0xFD

	// tdsDoneError marks the DONE token as reporting an error. Without it a
	// client reads the ERROR token and then waits for a completion that
	// never says anything went wrong.
	tdsDoneError uint16 = 0x0002

	// tdsErrorNumber 50000 is the conventional number for a user-raised
	// message (RAISERROR with no message id), so drivers surface it as an
	// ordinary server error rather than something they must special-case.
	tdsErrorNumber uint32 = 50000

	// tdsErrorClass 16 is the standard severity for a user-correctable
	// error, which is exactly what a policy denial is.
	tdsErrorClass byte = 16

	// maxTDSMessageChars bounds the message. The ERROR token declares its
	// message length in a USHORT and its body length in another, so an
	// operator-authored rule message long enough to overflow either would
	// otherwise produce a corrupt frame — a denial that desynchronizes the
	// client instead of explaining itself. Well below both limits.
	maxTDSMessageChars = 2000
)

// MSSQLError builds a TDS reply carrying an ERROR token followed by a
// DONE(error) token: the same shape a real SQL Server error takes, so the
// developer reads the operator's message in sqlcmd rather than watching the
// socket drop.
//
// The message is truncated on a CHARACTER boundary, never mid-UCS-2 unit, so
// a multi-byte rune cannot be split into an invalid encoding.
func MSSQLError(msg string) []byte {
	msgUCS2 := ucs2(truncateChars(msg, maxTDSMessageChars))

	// ERROR token body: Number(4) State(1) Class(1) MsgLen(2) Msg
	// ServerNameLen(1) ProcNameLen(1) LineNumber(4).
	body := make([]byte, 0, 14+len(msgUCS2))
	body = binary.LittleEndian.AppendUint32(body, tdsErrorNumber)
	body = append(body, 1)             // state
	body = append(body, tdsErrorClass) // severity
	body = binary.LittleEndian.AppendUint16(body, uint16(len(msgUCS2)/2))
	body = append(body, msgUCS2...)
	body = append(body, 0) // server name length
	body = append(body, 0) // procedure name length
	body = binary.LittleEndian.AppendUint32(body, 1)

	stream := make([]byte, 0, 3+len(body)+13)
	stream = append(stream, tdsTokenError)
	stream = binary.LittleEndian.AppendUint16(stream, uint16(len(body)))
	stream = append(stream, body...)

	// DONE: Status(2) CurCmd(2) DoneRowCount(8). The row count is 8 bytes on
	// TDS 7.2+, which every supported SQL Server speaks.
	stream = append(stream, tdsTokenDone)
	stream = binary.LittleEndian.AppendUint16(stream, tdsDoneError)
	stream = binary.LittleEndian.AppendUint16(stream, 0)
	stream = binary.LittleEndian.AppendUint64(stream, 0)

	out := make([]byte, 0, headerLen+len(stream))
	out = append(out, 0x04, 0x01) // reply packet, EOM
	out = binary.BigEndian.AppendUint16(out, uint16(headerLen+len(stream)))
	out = append(out, 0, 0, 1, 0) // SPID, packet id, window
	return append(out, stream...)
}

// headerLen is the fixed size of a TDS packet header.
const headerLen = 8

// truncateChars limits s to at most max runes, cutting on a character
// boundary. It scans with an early exit rather than allocating a []rune, so a
// large input costs only up to max runes of work.
func truncateChars(s string, max int) string {
	count := 0
	for idx := range s {
		if count == max {
			return s[:idx]
		}
		count++
	}
	return s
}

// ucs2 encodes s as little-endian UCS-2, the string encoding TDS uses.
func ucs2(s string) []byte {
	runes := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		out = binary.LittleEndian.AppendUint16(out, r)
	}
	return out
}

// PostgresError builds an ErrorResponse ('E') message.
//
// Wire format: each field is a one-byte type code and a NUL-terminated value,
// and a zero byte terminates the set.
// https://www.postgresql.org/docs/current/protocol-error-fields.html
//
// Severity is FATAL rather than ERROR because the connection is about to
// close. Reporting ERROR would leave psql waiting for a ReadyForQuery that
// never arrives, showing the user a hang instead of the message.
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
// It sets Connection: close because the caller is about to drop the socket.
// Without that header a keep-alive client waits for a second response that
// never comes.
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
