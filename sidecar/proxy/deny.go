package proxy

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"github.com/hoophq/hoop/sidecar/inspect"
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
func (ProtocolDenyWriter) Deny(proto inspect.Protocol, dir inspect.Direction, msg string) []byte {
	if msg == "" {
		msg = "denied by policy"
	}
	switch proto {
	case inspect.Postgres:
		return PostgresError(msg)
	case inspect.MSSQL:
		return MSSQLError(msg)
	case inspect.MySQL:
		return MySQLError(msg)
	case inspect.HTTP:
		return HTTPForbidden(msg)
	case inspect.GRPC:
		// Deliberately no frame. A gRPC denial is a trailers-only
		// response with grpc-status and the operator's message, written
		// by the lane that terminates HTTP/2 (ADR-0013); this relay
		// never carries a grpc lane, and a frame injected into a
		// multiplexed HTTP/2 connection mid-stream would corrupt it.
		return nil
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

// MySQL ERR_Packet constants for a synthesized server error.
// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_basic_err_packet.html
const (
	// mysqlErrPacketMarker is the first payload byte of an ERR_Packet. It
	// is what tells the client this is not an OK or a result set.
	mysqlErrPacketMarker byte = 0xFF

	// mysqlDenySeq is the sequence id of the reply.
	//
	// Sequence ids restart at 0 for every command, and the client's
	// COM_QUERY (or COM_STMT_EXECUTE) that we are refusing WAS that 0. A
	// reply must therefore be 1, or the driver reports "commands out of
	// sync" and discards the packet — the developer would see a protocol
	// error instead of the operator's message, which is the whole point of
	// synthesizing a frame rather than dropping the socket.
	mysqlDenySeq byte = 1

	// mysqlDenyErrno 1142 is ER_TABLEACCESS_DENIED_ERROR, "command denied
	// to user".
	//
	// Not 1045 (ER_ACCESS_DENIED_ERROR), which is tempting because it reads
	// as "access denied": 1045 is the HANDSHAKE failure, and drivers and
	// connection pools special-case it as bad credentials. A pool that sees
	// it mid-session evicts the connection and re-dials, and some CLIs
	// re-prompt for a password. 1142 is the per-statement authorization
	// failure, which is exactly what a policy denial is, and it leaves the
	// session usable for the next statement.
	mysqlDenyErrno uint16 = 1142

	// mysqlDenySQLState 42000 is syntax_error_or_access_rule_violation, the
	// state MySQL itself pairs with 1142. It is the same family as the
	// 42501 the Postgres path sends, so a client-side handler keyed on the
	// SQLSTATE class behaves alike on both.
	mysqlDenySQLState = "42000"

	// maxMySQLMessageChars bounds the message.
	//
	// The packet header declares its payload length in THREE bytes, so a
	// payload over 16 MiB-1 cannot be described and the server would have
	// to split it across frames. An operator-authored rule message must
	// never reach that: at 4 bytes per rune this caps the payload near 8
	// KiB, three orders of magnitude below the limit, so the length field
	// is always exact and no continuation frame is ever needed.
	maxMySQLMessageChars = 2000
)

// MySQLError builds an ERR_Packet: the same frame a real server sends when it
// refuses a statement, so the developer reads the operator's message in the
// mysql CLI as
//
//	ERROR 1142 (42000): destructive statements are not permitted on appdb
//
// rather than "Lost connection to MySQL server during query", which names
// nothing they can act on and looks like an outage worth paging about.
//
// The '#' marker and the five-byte SQLSTATE that follows it are only present
// when the client negotiated CLIENT_PROTOCOL_41. We always send them: every
// driver from MySQL 4.1 onward sets that flag, and the sidecar never
// completes a handshake with one that does not.
//
// The message is truncated on a RUNE boundary so a multi-byte character
// cannot be split into invalid UTF-8 mid-packet.
func MySQLError(msg string) []byte {
	text := truncateChars(msg, maxMySQLMessageChars)

	// Payload: marker(1) errno(2) '#'(1) sqlstate(5) message.
	payload := make([]byte, 0, 9+len(text))
	payload = append(payload, mysqlErrPacketMarker)
	payload = binary.LittleEndian.AppendUint16(payload, mysqlDenyErrno)
	payload = append(payload, '#')
	payload = append(payload, mysqlDenySQLState...)
	// No terminator: the message runs to the end of the packet, and its
	// length is the declared payload length minus the fixed prefix.
	payload = append(payload, text...)

	out := make([]byte, 0, mysqlHeaderLen+len(payload))
	n := uint32(len(payload))
	out = append(out, byte(n), byte(n>>8), byte(n>>16))
	out = append(out, mysqlDenySeq)
	return append(out, payload...)
}

// mysqlHeaderLen is the fixed size of a MySQL packet header: a three-byte
// little-endian payload length and a one-byte sequence id.
const mysqlHeaderLen = 4

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
