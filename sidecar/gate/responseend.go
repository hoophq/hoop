package gate

import (
	"encoding/binary"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// ResponseEnd counts the protocol's end-of-response markers in the server
// stream of one connection. A serial mask scope ends a masked response at its
// own marker, never at the next client statement.
//
// It reads every server byte from the first one, because it must stay on the
// protocol's framing. A scanner that loses the framing reports no end again:
// the count then never returns to zero, which fails closed.
type ResponseEnd interface {
	// Ended reports how many end-of-response messages data completes.
	Ended(data []byte) int
}

// requestCounter counts the responses the client's bytes ask for, one per
// marker the server will send, from the first client byte.
type requestCounter interface {
	// Requests reports how many responses data asks for. ok is false when
	// the count can no longer be known: lost framing, or a message whose
	// answer does not pair with one marker.
	Requests(data []byte) (n int, ok bool)
}

// responseEndFor is the marker scanner and request counter of a protocol, or
// nils when the gate follows none. Only framings a few lines can follow are
// here: a scanner that misreads a frame would end a response early, which
// fails open.
//
//   - postgres: the server's ReadyForQuery ('Z'), one for the startup
//     message and for every Query, Sync and FunctionCall.
//
// mssql is not here: its lane has no AI mask scope, because the pinned
// rewriter forwards some result data unmasked without reporting it. mysql (the end of a result set depends on negotiated capabilities and
// on a 0xFE first byte a row may also carry), oracle (TNS markers and
// continuations) and clickhouse (block payloads with no length) have none.
func responseEndFor(p inspect.Protocol) (ResponseEnd, requestCounter) {
	switch p {
	case inspect.Postgres:
		return &pgEnd{}, &pgRequests{}
	}
	return nil, nil
}

// ResponseEndKnown reports a protocol whose end-of-response marker a serial
// mask scope reads. On any other serial lane a masked statement's plan lasts
// for the connection, which then takes no further statement.
func ResponseEndKnown(p inspect.Protocol) bool {
	end, _ := responseEndFor(p)
	return end != nil
}

// framed follows a stream of messages that each start with a fixed header
// carrying the message length.
type framed struct {
	hdr  []byte
	skip int
	// pending marks the message being skipped as counted. It counts once
	// its last byte arrived, so nothing of that message is still in
	// flight.
	pending bool
	lost    bool
}

// scan walks data and counts the messages parse marks. size is the header
// size; parse reads one header into the body length that follows it and
// whether the message counts, or ok false for a header the framing cannot
// hold.
func (f *framed) scan(data []byte, size int, parse func(hdr []byte) (body int, count, ok bool)) int {
	n := 0
	for i := 0; i < len(data) && !f.lost; {
		if f.skip > 0 {
			k := min(f.skip, len(data)-i)
			f.skip -= k
			i += k
			if f.skip == 0 && f.pending {
				n, f.pending = n+1, false
			}
			continue
		}
		take := min(size-len(f.hdr), len(data)-i)
		f.hdr = append(f.hdr, data[i:i+take]...)
		i += take
		if len(f.hdr) < size {
			break
		}
		body, count, ok := parse(f.hdr)
		f.hdr = f.hdr[:0]
		if !ok {
			f.lost = true
			break
		}
		f.skip = body
		if count {
			if body == 0 {
				n++
			} else {
				f.pending = true
			}
		}
	}
	return n
}

// pgEnd follows pgwire server messages: a tag byte and an int32 length that
// counts itself.
type pgEnd struct {
	f       framed
	started bool
}

func (s *pgEnd) Ended(data []byte) int {
	if !s.started {
		// The answers to SSLRequest and GSSENCRequest are one untagged
		// byte each, before the first tagged message.
		for len(data) > 0 && (data[0] == 'N' || data[0] == 'S' || data[0] == 'G') {
			data = data[1:]
		}
		if len(data) == 0 {
			return 0
		}
		s.started = true
	}
	return s.f.scan(data, 5, func(hdr []byte) (int, bool, bool) {
		n := binary.BigEndian.Uint32(hdr[1:5])
		if n < 4 || n > 1<<30 {
			return 0, false, false
		}
		return int(n) - 4, hdr[0] == 'Z', true
	})
}

// pgRequests follows pgwire client messages: untagged ones (int32 length,
// int32 code) until the startup message, then tagged ones.
type pgRequests struct {
	f      framed
	tagged bool
	untag  []byte
	skip   int
}

// Untagged request codes that the server answers with one byte, or not at
// all.
const (
	pgSSLRequest    = 80877103
	pgGSSENCRequest = 80877104
	pgCancelRequest = 80877102
	pgProtocol3     = 196608
)

func (s *pgRequests) Requests(data []byte) (int, bool) {
	n := 0
	for !s.tagged && len(data) > 0 {
		if s.skip > 0 {
			k := min(s.skip, len(data))
			s.skip -= k
			data = data[k:]
			continue
		}
		take := min(8-len(s.untag), len(data))
		s.untag = append(s.untag, data[:take]...)
		data = data[take:]
		if len(s.untag) < 8 {
			return n, true
		}
		size := int(binary.BigEndian.Uint32(s.untag[0:4]))
		code := binary.BigEndian.Uint32(s.untag[4:8])
		s.untag = s.untag[:0]
		if size < 8 || size > 1<<20 {
			return n, false
		}
		s.skip = size - 8
		switch code {
		case pgSSLRequest, pgGSSENCRequest, pgCancelRequest:
		case pgProtocol3:
			// The startup message: the server ends authentication with
			// a ReadyForQuery.
			n++
			s.tagged = true
		default:
			return n, false
		}
	}
	if s.tagged && s.skip > 0 {
		k := min(s.skip, len(data))
		s.skip -= k
		data = data[k:]
	}
	n += s.f.scan(data, 5, func(hdr []byte) (int, bool, bool) {
		size := binary.BigEndian.Uint32(hdr[1:5])
		if size < 4 || size > 1<<30 {
			return 0, false, false
		}
		switch hdr[0] {
		case 'Q', 'S', 'F':
			return int(size) - 4, true, true
		}
		return int(size) - 4, false, true
	})
	return n, !s.f.lost
}
