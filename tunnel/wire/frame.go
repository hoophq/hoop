// Package wire defines the framing used between the tunnel client and the
// gateway over a single WebSocket connection.
//
// One WebSocket binary message carries exactly one Frame. The format is
// deliberately small and self-describing so that the same encoder works for
// both the client and the gateway:
//
//	+---------+---------+------------------+---------+----------+----------+
//	| version | type    | stream_id        | name_len| name...  | payload  |
//	|  1 byte | 1 byte  | 4 bytes (BE u32) | 2 bytes | name_len | rest     |
//	+---------+---------+------------------+---------+----------+----------+
//
// Stream IDs are allocated by the client and are unique within a single
// tunnel session. The gateway echoes them back on all frames belonging to a
// stream. Stream 0 is reserved for session-scoped control frames (currently
// only used by FrameTypePing / FrameTypePong).
//
// The name field is meaningful on FrameTypeStreamOpen; on all other frames it
// is empty (name_len == 0). Keeping the field on every frame keeps the
// encoder branch-free and gives us room to repurpose it later (e.g. for
// per-frame metadata) without bumping the version.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Version is the current wire format version. Incompatible changes must bump
// this and both sides must refuse to talk to a peer on a different version.
const Version uint8 = 1

// FrameType identifies the semantics of a frame's payload.
type FrameType uint8

const (
	// FrameTypeInvalid is the zero value and must never appear on the wire.
	FrameTypeInvalid FrameType = 0

	// FrameTypeStreamOpen requests a new TCP stream to the named connection.
	// payload: empty.
	// name:    connection name (non-empty).
	FrameTypeStreamOpen FrameType = 1

	// FrameTypeStreamData carries TCP payload bytes for an established stream
	// in either direction.
	// payload: TCP bytes.
	// name:    empty.
	FrameTypeStreamData FrameType = 2

	// FrameTypeStreamClose signals that the sender will not send more data on
	// this stream. The receiver should drain pending data and tear down its
	// half. Either side may send it.
	// payload: empty.
	// name:    empty.
	FrameTypeStreamClose FrameType = 3

	// FrameTypeStreamError signals an abnormal stream termination. The
	// payload carries a human-readable error string (UTF-8). The stream is
	// considered closed in both directions after this frame.
	// payload: UTF-8 error string.
	// name:    empty.
	FrameTypeStreamError FrameType = 4

	// FrameTypePing is a session-level liveness probe. The receiver must
	// reply with a FrameTypePong carrying the same payload.
	// stream_id: 0.
	// payload:   opaque echo bytes (may be empty).
	// name:      empty.
	FrameTypePing FrameType = 5

	// FrameTypePong is the reply to FrameTypePing.
	FrameTypePong FrameType = 6
)

func (t FrameType) String() string {
	switch t {
	case FrameTypeStreamOpen:
		return "stream-open"
	case FrameTypeStreamData:
		return "stream-data"
	case FrameTypeStreamClose:
		return "stream-close"
	case FrameTypeStreamError:
		return "stream-error"
	case FrameTypePing:
		return "ping"
	case FrameTypePong:
		return "pong"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

// ControlStreamID is the reserved stream id for session-scoped control
// traffic (ping / pong). Per-stream frames must use a non-zero id.
const ControlStreamID uint32 = 0

// Fixed header size: version(1) + type(1) + stream_id(4) + name_len(2).
const headerSize = 8

// MaxNameLen caps the connection-name field. The wire format reserves 16 bits
// but no real connection name approaches this; we enforce a tighter cap to
// catch garbage early.
const MaxNameLen = 255

// MaxFrameSize is a sanity ceiling for a decoded frame. WebSocket message
// fragmentation is the peer's responsibility; we just refuse oversized frames
// to avoid runaway allocations from a hostile peer.
const MaxFrameSize = 1 << 20 // 1 MiB

// Frame is a single wire frame, decoded.
type Frame struct {
	Type     FrameType
	StreamID uint32
	Name     string
	Payload  []byte
}

// Errors returned by Decode. Callers may use errors.Is for branching.
var (
	ErrShortFrame    = errors.New("wire: frame shorter than header")
	ErrVersion       = errors.New("wire: unsupported version")
	ErrNameTooLong   = errors.New("wire: name exceeds MaxNameLen")
	ErrFrameTooLarge = errors.New("wire: frame exceeds MaxFrameSize")
	ErrTruncated     = errors.New("wire: frame truncated (name or payload shorter than declared)")
)

// Encode serializes f into a new buffer suitable for a single WebSocket
// binary message. It returns an error only for invalid inputs (over-long
// name, zero version mismatch, etc.) — no I/O happens here.
func Encode(f Frame) ([]byte, error) {
	if len(f.Name) > MaxNameLen {
		return nil, fmt.Errorf("%w: %d bytes", ErrNameTooLong, len(f.Name))
	}
	size := headerSize + len(f.Name) + len(f.Payload)
	if size > MaxFrameSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, size)
	}
	out := make([]byte, size)
	out[0] = Version
	out[1] = byte(f.Type)
	binary.BigEndian.PutUint32(out[2:6], f.StreamID)
	binary.BigEndian.PutUint16(out[6:8], uint16(len(f.Name)))
	copy(out[headerSize:], f.Name)
	copy(out[headerSize+len(f.Name):], f.Payload)
	return out, nil
}

// Decode parses a single WebSocket binary message into a Frame. The returned
// Frame's Payload is a sub-slice of buf; callers that retain it past the next
// read must copy it themselves.
func Decode(buf []byte) (Frame, error) {
	if len(buf) < headerSize {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrShortFrame, len(buf))
	}
	if len(buf) > MaxFrameSize {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(buf))
	}
	v := buf[0]
	if v != Version {
		return Frame{}, fmt.Errorf("%w: got %d want %d", ErrVersion, v, Version)
	}
	nameLen := binary.BigEndian.Uint16(buf[6:8])
	if int(nameLen) > MaxNameLen {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrNameTooLong, nameLen)
	}
	if len(buf) < headerSize+int(nameLen) {
		return Frame{}, fmt.Errorf("%w: declared name_len=%d, buf=%d", ErrTruncated, nameLen, len(buf))
	}
	return Frame{
		Type:     FrameType(buf[1]),
		StreamID: binary.BigEndian.Uint32(buf[2:6]),
		Name:     string(buf[headerSize : headerSize+nameLen]),
		Payload:  buf[headerSize+nameLen:],
	}, nil
}
