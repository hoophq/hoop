// Package mysqltypes holds the minimal MySQL wire-protocol framing shared by
// every hoop component that relays MySQL bytes onto the packet stream: the
// `hoop connect` local proxy (client/proxy) and the tunnel's per-flow pipe
// (tunnel/client).
//
// Only framing lives here. Authentication, TLS and redaction belong to the
// agent-side proxy (libhoop), which is the single component that ever speaks
// to a real MySQL server.
//
// Why framing must be shared: the agent's MySQL proxy consumes one whole
// packet per Write (it decodes a packet from the buffer it is handed), so a
// relay that forwards arbitrary TCP chunks desynchronises it. Both relays
// therefore have to re-frame the client stream identically, and duplicating
// that logic is how the two paths drift.
package mysqltypes

import (
	"fmt"
	"io"
)

// HeaderSize is the fixed MySQL packet header: a 3-byte little-endian payload
// length followed by a 1-byte sequence id.
const HeaderSize = 4

// MaxPacketSize bounds a single packet's payload. MySQL's protocol maximum is
// 16 MiB - 1 (a payload of exactly 0xFFFFFF signals a continuation packet), so
// any length at or above this bound means the stream desynchronised.
const MaxPacketSize = 1<<24 - 1

// Packet is one framed MySQL protocol packet.
type Packet struct {
	// Seq is the packet sequence id, reset to 0 by the client at the start of
	// every command and incremented per packet within a command.
	Seq byte
	// Frame is the packet payload, excluding the 4-byte header.
	Frame []byte
}

// Encode returns the packet as it appears on the wire: header followed by
// payload.
func (p *Packet) Encode() []byte {
	out := make([]byte, HeaderSize+len(p.Frame))
	putUint24(out[0:3], uint32(len(p.Frame)))
	out[3] = p.Seq
	copy(out[HeaderSize:], p.Frame)
	return out
}

// Decode reads exactly one MySQL packet from r, blocking until the whole
// payload has arrived. It returns io.EOF only when the stream ends cleanly on
// a packet boundary; a truncated packet yields io.ErrUnexpectedEOF.
func Decode(r io.Reader) (*Packet, error) {
	var header [HeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	payloadLen := uint24(header[0:3])
	if payloadLen > MaxPacketSize {
		return nil, fmt.Errorf("mysql packet too large: %d bytes (max %d)", payloadLen, MaxPacketSize)
	}
	pkt := &Packet{Seq: header[3], Frame: make([]byte, payloadLen)}
	if _, err := io.ReadFull(r, pkt.Frame); err != nil {
		return nil, err
	}
	return pkt, nil
}

// CopyBuffer re-frames the MySQL stream from src onto dst, issuing exactly one
// dst.Write per protocol packet.
//
// dst is a packet-stream writer whose Write boundaries become hoop packet
// boundaries, which is why this cannot be an io.Copy: the agent-side proxy
// decodes one MySQL packet per write it receives.
//
// It returns nil when src ends cleanly on a packet boundary.
func CopyBuffer(dst io.Writer, src io.Reader) error {
	for {
		pkt, err := Decode(src)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		encoded := pkt.Encode()
		n, err := dst.Write(encoded)
		if err != nil {
			return err
		}
		// io.Writer permits a short write with a nil error. Forwarding a
		// truncated packet would desynchronise the peer's decoder, which
		// frames on the length prefix we just cut in half.
		if n != len(encoded) {
			return io.ErrShortWrite
		}
	}
}

func uint24(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

func putUint24(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
}
