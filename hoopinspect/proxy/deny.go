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
// Protocols with no in-band error frame before the handshake completes
// (MongoDB, raw TCP) return nil and the connection simply closes.
type ProtocolDenyWriter struct{}

// Deny implements DenyWriter.
func (ProtocolDenyWriter) Deny(proto hoopinspect.Protocol, dir hoopinspect.Direction, msg string) []byte {
	if msg == "" {
		msg = "denied by policy"
	}
	switch proto {
	case hoopinspect.Postgres:
		return PostgresError(msg)
	case hoopinspect.MySQL:
		return MySQLError(msg)
	case hoopinspect.MSSQL:
		return MSSQLError(msg)
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

// MySQLError builds an ERR packet.
//
// Layout: a 4-byte packet header, then 0xFF, a 2-byte error code, a 1-byte
// SQL-state marker '#', a 5-byte SQLSTATE, and the message.
// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_basic_err_packet.html
//
// Sequence id 1 assumes the denial follows a client command at sequence 0,
// which is the case for every COM_QUERY this gate inspects.
func MySQLError(msg string) []byte {
	// 1045 ER_ACCESS_DENIED_ERROR — clients render it as an access problem,
	// which is what a policy denial is.
	const errCode = 1045
	const sqlState = "28000"

	payload := make([]byte, 0, 9+len(msg))
	payload = append(payload, 0xFF)
	payload = binary.LittleEndian.AppendUint16(payload, errCode)
	payload = append(payload, '#')
	payload = append(payload, sqlState...)
	payload = append(payload, msg...)

	n := len(payload)
	out := make([]byte, 0, 4+n)
	out = append(out, byte(n), byte(n>>8), byte(n>>16), 1)
	return append(out, payload...)
}

// MSSQLError builds a TDS response containing an ERROR token.
//
// Layout: the 8-byte TDS packet header, then token 0xAA with a length,
// number, state, class, UCS-2 message, server name, procedure name and line
// number. Class 16 marks a user-correctable error, which is exactly what a
// policy denial is.
func MSSQLError(msg string) []byte {
	msgUCS2 := ucs2(msg)

	// number(4) state(1) class(1) msgLen(2) msg srvLen(1) procLen(1) line(4)
	tokenBody := make([]byte, 0, 14+len(msgUCS2))
	tokenBody = binary.LittleEndian.AppendUint32(tokenBody, 50000) // user-defined error number
	tokenBody = append(tokenBody, 1)                               // state
	tokenBody = append(tokenBody, 16)                              // class: user-correctable
	tokenBody = binary.LittleEndian.AppendUint16(tokenBody, uint16(len(msgUCS2)/2))
	tokenBody = append(tokenBody, msgUCS2...)
	tokenBody = append(tokenBody, 0) // server name length
	tokenBody = append(tokenBody, 0) // procedure name length
	tokenBody = binary.LittleEndian.AppendUint32(tokenBody, 1)

	body := make([]byte, 0, 3+len(tokenBody)+1)
	body = append(body, 0xAA) // ERROR token
	body = binary.LittleEndian.AppendUint16(body, uint16(len(tokenBody)))
	body = append(body, tokenBody...)
	body = append(body, 0xFD, 0x02, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0) // DONE

	out := make([]byte, 0, 8+len(body))
	out = append(out, 0x04) // response packet
	out = append(out, 0x01) // EOM
	out = binary.BigEndian.AppendUint16(out, uint16(8+len(body)))
	out = append(out, 0, 0, 1, 0) // SPID, packet id, window
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

// ucs2 encodes a string as UCS-2LE, the string encoding TDS uses.
//
// Characters outside the BMP are replaced rather than surrogate-encoded: a
// denial message is operator-authored ASCII in practice, and a mangled
// surrogate pair would corrupt the frame length.
func ucs2(s string) []byte {
	runes := []rune(s)
	out := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		if r > 0xFFFF {
			r = 0xFFFD
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(r))
	}
	return out
}
