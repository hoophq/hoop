// Package mysql decodes the MySQL client/server protocol far enough to
// recover the SQL a client sent.
//
// Envoy ships `envoy.filters.network.mysql_proxy`, but it does NOT parse SQL:
// it produces connection and command counters only. There is no statement
// text and no dynamic metadata to write a policy against. This package closes
// that gap.
//
// Wire format reference:
// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_basic_packets.html
//
// Every packet is:
//
//	uint24  payload length, little-endian
//	byte    sequence id
//	...     payload
//
// A payload of exactly 0xFFFFFF means the message continues in the next
// packet; the run ends with a packet shorter than 0xFFFFFF. This decoder
// reassembles those runs, because a large INSERT split at 16 MiB would
// otherwise be classified from a fragment.
//
// Commands carrying SQL:
//
//	0x03 COM_QUERY        — the statement, raw bytes to end of payload
//	0x16 COM_STMT_PREPARE — the statement being prepared
//
// The handshake is skipped: the server greeting and the client response are
// not commands and have no command byte.
package mysql

import (
	"errors"
	"strings"

	"github.com/hoophq/hoopinspect"
)

func init() { hoopinspect.Register(func() hoopinspect.Codec { return &Codec{} }) }

// MySQL command bytes. Only the SQL-bearing ones are named; the rest are
// skipped generically.
const (
	comQuery       = 0x03
	comStmtPrepare = 0x16
	comInitDB      = 0x02
	comFieldList   = 0x04
	comStmtExecute = 0x17
	comStmtClose   = 0x19
	comQuit        = 0x01
	comPing        = 0x0e
)

const (
	headerLen = 4

	// maxPayload is the largest single-packet payload; a payload of exactly
	// this size means "continued in the next packet".
	maxPayload = 0xFFFFFF

	// maxMessageLen bounds reassembly of a continued run.
	maxMessageLen = 64 << 20
)

// ErrMalformed means the stream is not valid MySQL protocol.
var ErrMalformed = errors.New("hoopinspect/mysql: malformed packet")

// Codec implements hoopinspect.Codec for MySQL.
//
// Stateful: it tracks the handshake phase (so the client's auth response is
// not mistaken for a command) and reassembles continued packet runs. One
// Codec per connection.
type Codec struct {
	// handshakeDone flips once the client has sent its first real command.
	// Before that, the first client packet is the handshake response, whose
	// first byte is a capability flag, not a command.
	handshakeDone bool

	pending []byte
}

func (c *Codec) Protocol() hoopinspect.Protocol { return hoopinspect.MySQL }

// Decode implements hoopinspect.Codec.
//
// Metadata keys:
//
//	"mysql.command" — "COM_QUERY" or "COM_STMT_PREPARE"
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

		payloadLen := int(uint32(data[pos]) | uint32(data[pos+1])<<8 | uint32(data[pos+2])<<16)
		seq := data[pos+3]
		total := headerLen + payloadLen

		if len(data)-pos < total {
			return stmts, pos, nil // partial packet
		}
		payload := data[pos+headerLen : pos+total]
		pos += total

		if len(c.pending)+len(payload) > maxMessageLen {
			c.pending = nil
			return stmts, pos, ErrMalformed
		}
		c.pending = append(c.pending, payload...)

		if payloadLen == maxPayload {
			continue // continued in the next packet
		}

		msg := c.pending
		c.pending = nil

		// The client's handshake response carries sequence id 1 and is not a
		// command. Everything after it starts a fresh command at seq 0.
		if !c.handshakeDone {
			if seq != 0 {
				continue // handshake response; skip it
			}
			c.handshakeDone = true
		}

		if len(msg) < 1 {
			continue
		}
		cmd := msg[0]
		if cmd != comQuery && cmd != comStmtPrepare {
			continue
		}

		text := strings.TrimRight(string(msg[1:]), "\x00")
		if strings.TrimSpace(text) == "" {
			continue
		}

		op, tables := hoopinspect.ClassifySQL(text)
		stmts = append(stmts, hoopinspect.Statement{
			Protocol:  hoopinspect.MySQL,
			Direction: hoopinspect.FromClient,
			Text:      text,
			Operation: op,
			Tables:    tables,
			Metadata:  map[string]string{"mysql.command": commandName(cmd)},
		})
	}
}

func commandName(cmd byte) string {
	switch cmd {
	case comQuery:
		return "COM_QUERY"
	case comStmtPrepare:
		return "COM_STMT_PREPARE"
	case comInitDB:
		return "COM_INIT_DB"
	case comFieldList:
		return "COM_FIELD_LIST"
	case comStmtExecute:
		return "COM_STMT_EXECUTE"
	case comStmtClose:
		return "COM_STMT_CLOSE"
	case comQuit:
		return "COM_QUIT"
	case comPing:
		return "COM_PING"
	}
	return "unknown"
}

// SkipHandshake tells the codec the handshake is already complete. Use it when
// inspection starts mid-connection (for example an Envoy filter attached to an
// established stream), so the first packet seen is treated as a command rather
// than as the client's auth response.
func (c *Codec) SkipHandshake() { c.handshakeDone = true }

// Sequence extracts the sequence id from a packet header. Exposed for callers
// correlating requests with responses.
func Sequence(packet []byte) (byte, bool) {
	if len(packet) < headerLen {
		return 0, false
	}
	return packet[3], true
}

// PayloadLen reads the declared payload length from a packet header.
func PayloadLen(packet []byte) (int, bool) {
	if len(packet) < headerLen {
		return 0, false
	}
	return int(uint32(packet[0]) | uint32(packet[1])<<8 | uint32(packet[2])<<16), true
}
