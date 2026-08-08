// Package mssql decodes Microsoft SQL Server's TDS protocol far enough to
// recover the SQL a client sent, and to refuse the one server reply that
// would take the connection away from the relay.
//
// Envoy has no TDS filter of any kind, so this protocol is entirely unpoliced
// by an Envoy+OPA layer today. That is a bigger gap than Postgres, which at
// least gets table-and-verb metadata out of `envoy.filters.network.postgres_proxy`.
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
// # Integrated authentication passes through untouched
//
// TDS gives the SSPI exchange its own packet type, 0x11, and the login its
// own, 0x10. That framing is what makes Kerberos work through this codec
// without a single line of Kerberos code: the AP-REQ a client's OS minted is
// opaque bytes in a packet type this decoder forwards verbatim, and the first
// 0x01/0x03 afterwards is where inspection begins. The boundary is the
// protocol's own message typing, not a guess about where login ended.
//
// A relay cannot do anything else here even if it wanted to. The SSPI blob is
// a service ticket bound to the server's SPN: relayable, never mintable and
// never editable. See DetectSSPI for reporting that a login is integrated.
//
// # Downstream TLS belongs to whatever fronts this
//
// This decoder reads plaintext TDS. Under TDS 8.0 (SQL Server 2022+,
// Encrypt=strict) the whole session is inside TLS from the first byte, which
// is ordinary TLS-on-connect and therefore something Envoy terminates
// natively. Under TDS 7.x the handshake is wrapped in 0x12 PRELOGIN packets,
// which Envoy cannot speak; that lane needs a terminator that does.
package mssql

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/hoophq/hoopinspect"
)

func init() { hoopinspect.Register(func() hoopinspect.Codec { return &Codec{} }) }

// TDS packet types.
const (
	pktSQLBatch    = 0x01
	pktRPCRequest  = 0x03
	pktReply       = 0x04
	pktLogin7      = 0x10
	pktSSPIMessage = 0x11
	pktPrelogin    = 0x12
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
	//
	// 16 MiB rather than something larger: a statement past this is
	// pathological, and the two alternatives to refusing it are both worse.
	// Forwarding it unchecked is a bypass; buffering without a bound hands a
	// client the relay's memory. This matches the cap libhoop's MSSQL proxy
	// already enforces, so the two agree on what "too big to inspect" means.
	maxMessageLen = 16 << 20
)

// ErrMalformed means the stream is not valid TDS.
var ErrMalformed = errors.New("hoopinspect/mssql: malformed packet")

// Codec implements hoopinspect.Codec for MSSQL.
//
// It is stateful in both directions: TDS messages span packets, so partial
// messages accumulate in `pending`, and the routing guard needs to know
// whether the login has finished. One Codec per connection; the registry
// hands out a factory for exactly this reason.
type Codec struct {
	pending []byte
	pendTyp byte

	// authDone flips on the first client query packet.
	//
	// It scopes the routing scan below to the login phase, which is both
	// where MS-TDS puts routing information and the only window where a
	// false positive is implausible: before the first query there are no
	// result rows on the wire whose bytes could imitate an ENVCHANGE token.
	// It also keeps the scan off the hot path entirely once a session is
	// doing work.
	authDone bool

	// Response-rewriting state, independent of the decode state above: the
	// gate drives Decode and Rewrite over separate copies of the stream.
	//
	// cols is the layout the most recent COLMETADATA described, held is the
	// tail of a packet that has not arrived whole, and noRewrite latches once
	// a type appears whose length this codec cannot compute.
	cols        []column
	held        []byte
	tokenBuf    []byte
	rawBuf      []byte
	seenColMeta bool
	noRewrite   bool
}

func (c *Codec) Protocol() hoopinspect.Protocol { return hoopinspect.MSSQL }

// Decode implements hoopinspect.Codec.
//
// Metadata keys:
//
//	"mssql.message" — "SQLBatch" or "RPCRequest"
//	"mssql.proc"    — "sp_executesql", only for a decoded RPC
func (c *Codec) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	if dir == hoopinspect.FromServer {
		return c.decodeServer(data)
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
		// packet type would buffer bulk-load traffic for no benefit, and
		// this is also what forwards the login: PRELOGIN (0x12), LOGIN7
		// (0x10) and every SSPI continuation (0x11) land here, get no
		// interpretation, and travel on untouched.
		if typ != pktSQLBatch && typ != pktRPCRequest {
			c.pending, c.pendTyp = nil, 0
			continue
		}

		// A query packet means the login finished. Everything the server
		// sends from here is data, so the routing scan retires.
		c.authDone = true

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

// decodeServer handles the upstream -> client direction.
//
// It yields no statements: response-side decoding of TDS token streams
// (COLMETADATA, ROW, NBCROW) is not implemented, so a result set travels
// through unread. What it does do is refuse a routing ENVCHANGE, which is the
// one server reply that ends the relay's visibility.
func (c *Codec) decodeServer(data []byte) ([]hoopinspect.Statement, int, error) {
	if c.authDone {
		return nil, len(data), nil
	}

	pos := 0
	for {
		if len(data)-pos < headerLen {
			// Consume only whole packets, so a routing ENVCHANGE split
			// across two reads is scanned once it is complete rather than
			// missed at the seam.
			return nil, pos, nil
		}

		typ := data[pos]
		length := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if length < headerLen {
			return nil, pos, ErrMalformed
		}
		if len(data)-pos < length {
			return nil, pos, nil
		}

		payload := data[pos+headerLen : pos+length]
		pos += length

		if typ != pktReply {
			continue
		}
		if srv, port, found := findRoutingEnvChange(payload); found {
			return nil, pos, fmt.Errorf(
				"%w: the server answered login with a routing redirect to %s:%s, "+
					"which every driver follows silently onto a socket this relay "+
					"does not hold; the session would continue with no policy, no "+
					"masking and no audit trail",
				hoopinspect.ErrStreamUnsafe, srv, port)
		}
	}
}

// TDS token-stream constants for the login response.
const (
	tokenEnvChange = 0xe3
	envTypeRouting = 20
	routingTCP     = 0
)

// findRoutingEnvChange reports whether a login response carries a routing
// ENVCHANGE, and where it points.
//
// # Why a scan rather than a token walk
//
// Walking the token stream exactly means implementing the length rule of
// every token that can precede this one (LOGINACK, INFO, ERROR, FEATUREEXTACK,
// SSPI, DONE), each with its own encoding. That is a lot of surface for a
// guard, and getting any one of them wrong desynchronizes the walk and MISSES
// the redirect — a silent bypass, the failure this exists to prevent.
//
// So it scans for the token byte and then validates the full structure at
// that offset. A false positive costs a refused connection an operator can
// see and diagnose; a false negative costs the entire control silently. The
// structural check is strict enough that a coincidence is implausible: the
// candidate must carry the routing type, a TCP protocol byte, and inner
// lengths that agree with the declared outer length.
//
// ENVCHANGE, from the token byte:
//
//	+0      BYTE    0xE3
//	+1..2   USHORT  length of everything after this field
//	+3      BYTE    type (20 = routing)
//	+4..5   USHORT  ValueLength: size of the RoutingData that follows
//	+6      BYTE    Protocol (0 = TCP)
//	+7..8   USHORT  Port
//	+9..10  USHORT  AlternateServer length, in UCS-2 CHARACTERS
//	+11..   UCS-2   AlternateServer
func findRoutingEnvChange(body []byte) (server, port string, found bool) {
	for i := range body {
		if body[i] != tokenEnvChange {
			continue
		}
		srv, prt, ok := parseRoutingEnvChange(body[i:])
		if ok {
			return srv, prt, true
		}
	}
	return "", "", false
}

// parseRoutingEnvChange validates one candidate ENVCHANGE and extracts the
// redirect target. Every bound is checked against the slice AND against the
// token's own declared length, so a stray 0xE3 in unrelated bytes has to
// satisfy four independent constraints to be mistaken for a redirect.
func parseRoutingEnvChange(b []byte) (server, port string, ok bool) {
	// Smallest possible routing ENVCHANGE: header through an empty server
	// name, plus the trailing OldValue length.
	const minLen = 13
	if len(b) < minLen {
		return "", "", false
	}

	tokenLen := int(binary.LittleEndian.Uint16(b[1:3]))
	if tokenLen < minLen-3 || 3+tokenLen > len(b) {
		return "", "", false
	}
	if b[3] != envTypeRouting {
		return "", "", false
	}

	valueLen := int(binary.LittleEndian.Uint16(b[4:6]))
	// RoutingData is Protocol(1) + Port(2) + a US_VARCHAR, and it has to fit
	// inside the token's own declared length.
	if valueLen < 5 || 6+valueLen > 3+tokenLen {
		return "", "", false
	}
	if b[6] != routingTCP {
		return "", "", false
	}

	portNum := binary.LittleEndian.Uint16(b[7:9])
	nameChars := int(binary.LittleEndian.Uint16(b[9:11]))
	nameBytes := nameChars * 2
	// The name must fit inside RoutingData exactly as declared: 1 protocol
	// byte + 2 port bytes + 2 length bytes + the name.
	if 5+nameBytes != valueLen || 11+nameBytes > len(b) {
		return "", "", false
	}

	return ucs2ToString(b[11 : 11+nameBytes]), fmt.Sprint(portNum), true
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
	// Username from the LOGIN7 UserName field. Empty under integrated auth,
	// where the identity lives inside the opaque SSPI blob instead.
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
// # What a relay can and cannot do with the answer
//
// UsesSSPI true means the credential on the wire is a service ticket bound to
// the server's SPN. It can be RELAYED — the 0x11 packets carrying it are
// forwarded verbatim by Decode — but never minted, edited or read. So this
// reports a fact for the audit trail and for a clear error message; it is not
// a decision point, and a relay that refuses integrated auth on seeing it
// would be refusing the case that works.
//
// The one thing it cannot give you is the principal: under integrated auth
// Username is empty, because the name is inside the ticket. Populating the
// actor for such a session means reading identity from whatever terminated
// the client's TLS, via proxy.Config.IdentityFn.
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

// IsSSPIMessage reports whether a packet is an SSPI continuation (0x11), the
// packet type that carries a Kerberos or NTLM exchange after LOGIN7.
//
// Decode forwards these untouched; this exists so a caller can COUNT them, or
// log that an integrated login is in progress, without reimplementing the
// header parse.
func IsSSPIMessage(packet []byte) bool {
	return len(packet) >= headerLen && packet[0] == pktSSPIMessage
}
