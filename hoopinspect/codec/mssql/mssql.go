// Package mssql decodes Microsoft SQL Server's TDS protocol far enough to
// recover the SQL a client sent.
//
// Envoy has no TDS filter of any kind, so this protocol is entirely unpoliced
// by an Envoy+OPA layer today.
//
// Wire format reference:
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/
//
// Every TDS packet carries an 8-byte header:
//
//	byte   type
//	byte   status (bit 0 = EOM, "end of message")
//	uint16 length, INCLUDING the 8-byte header, big-endian
//	uint16 SPID
//	byte   packet id
//	byte   window
//
// A single logical message may span multiple packets; the last one has the
// EOM status bit set. This decoder reassembles them before parsing, because a
// statement split across two packets would otherwise be classified from a
// fragment — the exact failure mode a policy cannot tolerate.
//
// Two message types carry SQL:
//
//	0x01 SQLBatch   — ALL_HEADERS block, then the statement as UCS-2LE.
//	0x03 RPCRequest — a proc call; when the proc is sp_executesql the first
//	                  NVARCHAR parameter holds the statement text.
//
// LOGIN7 (0x10) is decoded for its SSPI field only, to detect Kerberos /
// integrated auth. See DetectSSPI.
package mssql

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/hoophq/hoopinspect"
)

func init() { hoopinspect.Register(func() hoopinspect.Codec { return &Codec{} }) }

// TDS packet types.
const (
	pktSQLBatch   = 0x01
	pktRPCRequest = 0x03
	pktLogin7     = 0x10
	pktPrelogin   = 0x12
)

const (
	headerLen = 8

	// statusEOM marks the final packet of a multi-packet message.
	statusEOM = 0x01

	// sp_executesql is the system proc whose first parameter is a statement
	// string. This is how every parameterized query from .NET/JDBC arrives,
	// so a decoder that ignored RPC would miss most real traffic.
	procIDExecuteSQL = 10

	// typeNVarChar is the TYPE_INFO byte for NVARCHARTYPE.
	typeNVarChar = 0xe7

	// maxMessageLen bounds reassembly of a multi-packet message.
	maxMessageLen = 64 << 20
)

// ErrMalformed means the stream is not valid TDS.
var ErrMalformed = errors.New("hoopinspect/mssql: malformed packet")

// Codec implements hoopinspect.Codec for MSSQL.
//
// It is stateful: TDS messages span packets, so partial messages accumulate
// in `pending`. One Codec per connection. (Postgres and MySQL codecs are
// stateless values; this one is a pointer for that reason.)
type Codec struct {
	pending []byte
	pendTyp byte
}

func (c *Codec) Protocol() hoopinspect.Protocol { return hoopinspect.MSSQL }

// Decode implements hoopinspect.Codec.
//
// Metadata keys:
//
//	"mssql.message" — "SQLBatch" or "RPCRequest"
//	"mssql.proc"    — "sp_executesql", only for a decoded RPC
func (c *Codec) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	if dir != hoopinspect.FromClient {
		return nil, len(data), nil
	}

	var stmts []hoopinspect.Statement
	pos := 0

	for {
		if len(data)-pos < headerLen {
			return stmts, pos, nil
		}

		typ := data[pos]
		status := data[pos+1]
		length := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))

		if length < headerLen {
			return stmts, pos, ErrMalformed
		}
		if len(data)-pos < length {
			return stmts, pos, nil // packet not fully buffered
		}

		payload := data[pos+headerLen : pos+length]
		pos += length

		// Only these types carry anything we decode. Reassembling every
		// packet type would buffer bulk-load traffic for no benefit.
		if typ != pktSQLBatch && typ != pktRPCRequest {
			c.pending, c.pendTyp = nil, 0
			continue
		}

		if len(c.pending)+len(payload) > maxMessageLen {
			c.pending, c.pendTyp = nil, 0
			return stmts, pos, ErrMalformed
		}
		c.pending = append(c.pending, payload...)
		c.pendTyp = typ

		if status&statusEOM == 0 {
			continue // more packets to come
		}

		// EOM: the message is whole.
		msg := c.pending
		c.pending, c.pendTyp = nil, 0

		var text, proc string
		switch typ {
		case pktSQLBatch:
			text = decodeSQLBatch(msg)
		case pktRPCRequest:
			text, proc = decodeRPCRequest(msg)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}

		op, tables := hoopinspect.ClassifySQL(text)
		md := map[string]string{"mssql.message": msgName(typ)}
		if proc != "" {
			md["mssql.proc"] = proc
		}
		stmts = append(stmts, hoopinspect.Statement{
			Protocol:  hoopinspect.MSSQL,
			Direction: hoopinspect.FromClient,
			Text:      text,
			Operation: op,
			Tables:    tables,
			Metadata:  md,
		})
	}
}

func msgName(typ byte) string {
	if typ == pktSQLBatch {
		return "SQLBatch"
	}
	return "RPCRequest"
}

// decodeSQLBatch extracts the statement from a reassembled SQLBatch body.
//
// The body starts with ALL_HEADERS: a uint32 total length followed by that
// many bytes of header data. The statement follows as UCS-2LE.
//
// ALL_HEADERS is only present in the FIRST packet of a message. Since we
// reassemble before parsing, it is present exactly once at the front, and a
// length that overruns the body means it was absent (some drivers omit it) —
// in which case the whole body is the statement.
func decodeSQLBatch(body []byte) string {
	if len(body) < 4 {
		return ""
	}
	hdrLen := binary.LittleEndian.Uint32(body[:4])
	if int(hdrLen) >= 4 && int(hdrLen) <= len(body) {
		return ucs2ToString(body[hdrLen:])
	}
	return ucs2ToString(body)
}

// decodeRPCRequest extracts the statement from an sp_executesql call.
//
// Body layout: ALL_HEADERS, then NameLenProcID. A well-known proc is encoded
// as 0xFFFF followed by the uint16 proc id; a named proc is a UCS-2 string.
// Then option flags, then the parameter list. For sp_executesql the first
// parameter is the statement, an NVARCHAR.
//
// This is deliberately narrow: it recognizes sp_executesql and nothing else,
// returning "" for any other proc. A partial decode that guessed would be
// worse than no decode, because a policy would evaluate the wrong text.
func decodeRPCRequest(body []byte) (text, proc string) {
	if len(body) < 4 {
		return "", ""
	}
	hdrLen := binary.LittleEndian.Uint32(body[:4])
	if int(hdrLen) < 4 || int(hdrLen) > len(body) {
		return "", ""
	}
	b := body[hdrLen:]

	// NameLenProcID: 0xFFFF marks a well-known proc id.
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xff {
		return "", ""
	}
	if binary.LittleEndian.Uint16(b[2:4]) != procIDExecuteSQL {
		return "", ""
	}
	b = b[4:]

	// OptionFlags (uint16), then the first parameter:
	//   byte   name length (in UCS-2 chars)
	//   ...    name
	//   byte   status flags
	//   byte   TYPE_INFO
	if len(b) < 2 {
		return "", ""
	}
	b = b[2:]

	if len(b) < 1 {
		return "", ""
	}
	nameLen := int(b[0]) * 2 // UCS-2
	if len(b) < 1+nameLen+2 {
		return "", ""
	}
	b = b[1+nameLen+1:] // skip name and status flag

	if b[0] != typeNVarChar {
		return "", ""
	}
	b = b[1:]

	// NVARCHAR TYPE_INFO: uint16 max length, then a 5-byte COLLATION.
	if len(b) < 7 {
		return "", ""
	}
	b = b[7:]

	// TYPE_VARBYTE: uint16 actual length, then the UCS-2 payload. 0xFFFF is
	// the NULL sentinel.
	if len(b) < 2 {
		return "", ""
	}
	dataLen := int(binary.LittleEndian.Uint16(b[:2]))
	if dataLen == 0xffff {
		return "", ""
	}
	b = b[2:]
	if dataLen > len(b) {
		dataLen = len(b) // truncated; decode what arrived
	}
	return ucs2ToString(b[:dataLen]), "sp_executesql"
}

// ucs2ToString converts UCS-2LE (TDS's string encoding) to UTF-8. A trailing
// odd byte is dropped rather than producing a replacement char.
func ucs2ToString(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	n := len(b) / 2
	u := make([]uint16, n)
	for i := range n {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

// LoginInfo reports what a LOGIN7 packet says about how the client intends to
// authenticate.
type LoginInfo struct {
	// Username from the LOGIN7 UserName field. Empty under integrated auth.
	Username string

	// Database the client is connecting to, when stated.
	Database string

	// AppName the driver reported.
	AppName string

	// UsesSSPI is true when the login carries an SSPI blob, meaning
	// integrated authentication: Kerberos or NTLM.
	UsesSSPI bool
}

// DetectSSPI parses a LOGIN7 packet and reports how the client is
// authenticating.
//
// This exists to fail loudly rather than silently. A proxy that rewrites
// credentials cannot work against Kerberos: the SSPI blob is a service ticket
// bound to the server's SPN, so it can be relayed but never minted or
// modified. If Extended Protection (channel binding) is on, the authenticator
// is bound to the TLS channel and ANY interposition invalidates it.
//
// Callers should check UsesSSPI at login and refuse the session with a clear
// message instead of letting the client fail with an opaque auth error deep in
// the handshake.
//
// Returns ok=false when the packet is not a complete LOGIN7.
func DetectSSPI(packet []byte) (info LoginInfo, ok bool) {
	if len(packet) < headerLen || packet[0] != pktLogin7 {
		return LoginInfo{}, false
	}
	length := int(binary.BigEndian.Uint16(packet[2:4]))
	if length < headerLen || len(packet) < length {
		return LoginInfo{}, false
	}
	body := packet[headerLen:length]

	// LOGIN7 fixed header is 94 bytes: 6 uint32 fields, 4 flag bytes,
	// int32 timezone, uint32 LCID, then the offset/length pairs.
	const fixedLen = 94
	if len(body) < fixedLen {
		return LoginInfo{}, false
	}

	// Offsets are relative to the start of the LOGIN7 body.
	read := func(off int) (offset, length int) {
		return int(binary.LittleEndian.Uint16(body[off:])),
			int(binary.LittleEndian.Uint16(body[off+2:]))
	}
	str := func(off int) string {
		o, l := read(off)
		l *= 2 // UCS-2
		if o <= 0 || l <= 0 || o+l > len(body) {
			return ""
		}
		return ucs2ToString(body[o : o+l])
	}

	// Field offset table within the fixed header (see MS-TDS 2.2.6.4).
	const (
		offUserName = 40
		offAppName  = 48
		offDatabase = 64
		offSSPI     = 78
	)

	info.Username = str(offUserName)
	info.AppName = str(offAppName)
	info.Database = str(offDatabase)

	sspiOff, sspiLen := read(offSSPI)
	info.UsesSSPI = sspiLen > 0 && sspiOff > 0 && sspiOff+sspiLen <= len(body)

	return info, true
}

// IsPrelogin reports whether a packet is a TDS PRELOGIN. Useful for a caller
// that wants to observe encryption negotiation before deciding whether it can
// see anything at all.
func IsPrelogin(packet []byte) bool {
	return len(packet) >= headerLen && packet[0] == pktPrelogin
}
