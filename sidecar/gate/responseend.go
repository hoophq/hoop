package gate

import (
	"encoding/binary"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// ResponseEnd finds the protocol's end-of-response marker in the server
// stream of one connection. A serial mask scope ends a response there and not
// at the next client statement, so a statement pipelined into a masked
// response is refused rather than let the rest of that response out unmasked.
//
// It reads every server byte from the first one, because it must stay on the
// protocol's framing. A scanner that loses the framing reports no end again:
// the plan then lasts for the connection, which fails closed.
type ResponseEnd interface {
	// Ended reports whether data completes an end-of-response message.
	Ended(data []byte) bool
}

// responseEndFor is the marker scanner of a protocol, or nil when the gate
// reads none. Only framings a few lines can follow are here: a scanner that
// misreads a frame would end a response early, which fails open.
//
//   - postgres: ReadyForQuery ('Z'), which follows every simple query and
//     every Sync.
//   - mssql: the TDS packet whose status carries End Of Message, the last
//     packet of every server message.
//
// mysql (the end of a result set depends on negotiated capabilities and
// on a 0xFE first byte a row may also carry), oracle (TNS markers and
// continuations) and clickhouse (block payloads with no length) have none.
func responseEndFor(p inspect.Protocol) ResponseEnd {
	switch p {
	case inspect.Postgres:
		return &pgEnd{}
	case inspect.MSSQL:
		return &tdsEnd{}
	}
	return nil
}

// ResponseEndKnown reports a protocol whose end-of-response marker a serial
// mask scope reads. On any other serial lane a masked statement's plan lasts
// for the connection, which then takes no further statement.
func ResponseEndKnown(p inspect.Protocol) bool { return responseEndFor(p) != nil }

// framed follows a stream of messages that each start with a fixed header
// carrying the message length.
type framed struct {
	hdr  []byte
	skip int
	// endPending marks the message being skipped as an end marker. The end
	// counts once its last byte arrived, so nothing of that message is
	// still in flight.
	endPending bool
	lost       bool
}

// scan walks data. size is the header size; parse reads one header into the
// body length that follows it and whether the message ends a response, or
// ok false for a header the framing cannot hold.
func (f *framed) scan(data []byte, size int, parse func(hdr []byte) (body int, end, ok bool)) bool {
	ended := false
	for i := 0; i < len(data) && !f.lost; {
		if f.skip > 0 {
			n := min(f.skip, len(data)-i)
			f.skip -= n
			i += n
			if f.skip == 0 && f.endPending {
				ended, f.endPending = true, false
			}
			continue
		}
		take := min(size-len(f.hdr), len(data)-i)
		f.hdr = append(f.hdr, data[i:i+take]...)
		i += take
		if len(f.hdr) < size {
			break
		}
		body, end, ok := parse(f.hdr)
		f.hdr = f.hdr[:0]
		if !ok {
			f.lost = true
			break
		}
		f.skip = body
		if end {
			if body == 0 {
				ended = true
			} else {
				f.endPending = true
			}
		}
	}
	return ended
}

// pgEnd follows pgwire messages: a tag byte and an int32 length that counts
// itself.
type pgEnd struct {
	f       framed
	started bool
}

func (s *pgEnd) Ended(data []byte) bool {
	if !s.started && len(data) > 0 {
		s.started = true
		// The answer to SSLRequest or GSSENCRequest is one untagged byte.
		if data[0] == 'N' || data[0] == 'S' {
			data = data[1:]
		}
	}
	return s.f.scan(data, 5, func(hdr []byte) (int, bool, bool) {
		n := binary.BigEndian.Uint32(hdr[1:5])
		if n < 4 || n > 1<<30 {
			return 0, false, false
		}
		return int(n) - 4, hdr[0] == 'Z', true
	})
}

// tdsEnd follows TDS packets: an 8-byte header whose uint16 length counts the
// header, and a status byte whose low bit ends the message.
type tdsEnd struct{ f framed }

func (s *tdsEnd) Ended(data []byte) bool {
	return s.f.scan(data, 8, func(hdr []byte) (int, bool, bool) {
		n := int(binary.BigEndian.Uint16(hdr[2:4]))
		if n < 8 {
			return 0, false, false
		}
		return n - 8, hdr[1]&0x01 != 0, true
	})
}
