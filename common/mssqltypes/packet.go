package mssqltypes

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

// headerSize is the fixed TDS packet header:
// [type(1), status(1), length(2), spid(2), id(1), window(1)].
// The length field counts this header, so it can never be smaller.
const headerSize = 8

// Packet represents a TDS Packet
type Packet struct {
	// [type(1), status(1), length(2), spid(2), id(1), window(1)]
	header [8]byte

	// Payload of the packet
	Frame []byte
}

// New creates a packet type setting it's size and frame.
// It use hard-coded values for some header fields, it may not be useful
// depending on the flow this function will be used.
func New(typ PacketType, data []byte) *Packet {
	p := &Packet{header: NewHeader(typ, len(data))}
	p.Frame = data

	// if resetSession {
	// 	switch packetType {
	// 	// Reset session can only be set on the following packet types.
	// 	case packSQLBatch, packRPCRequest, packTransMgrReq:
	// 		status = 0x8
	// 	}
	// }
	return p
}

// NewHeader returns the packet header with hard-coded values
// it may not be useful depending on the flow being used.
func NewHeader(packetType PacketType, dataSize int) (header [8]byte) {
	header[0] = byte(packetType)
	// status (hard-coded)
	header[1] = 0x01
	// length
	binary.BigEndian.PutUint16(header[2:4], uint16(dataSize)+headerSize)

	// spid (hard-coded)
	header[4] = 0x00
	header[5] = 0x00

	// packet id (hard-coded - it seems to be safe to not implement it)
	header[6] = 0x01
	// window (hard-coded)
	header[7] = 0x00
	return
}

func (p *Packet) Encode() []byte {
	dst := make([]byte, p.Length())
	copy(dst, append(p.header[:], p.Frame...))
	return dst
}

func (p *Packet) Length() uint16 {
	var pktLen [2]byte
	copy(pktLen[:], p.header[2:4])
	return binary.BigEndian.Uint16(pktLen[:])
}

func (p *Packet) Dump()            { fmt.Println(hex.Dump(p.Encode())) }
func (p *Packet) Type() PacketType { return PacketType(p.header[0]) }

func Decode(data io.Reader) (*Packet, error) {
	p := &Packet{}
	_, err := io.ReadFull(data, p.header[:])
	if err != nil {
		return nil, err
	}
	if _, ok := packetTypeMap[PacketType(p.header[0])]; !ok {
		return nil, fmt.Errorf("decoded an unknown packet type [%X]", p.header[0])
	}
	// Length counts the 8-byte header itself. Validate before subtracting:
	// an undersized value would underflow the unsigned length and make the
	// reader block for ~64 KiB of payload that will never arrive.
	total := p.Length()
	if total < headerSize {
		return nil, fmt.Errorf("invalid TDS packet length: %d", total)
	}
	p.Frame = make([]byte, total-headerSize)
	_, err = io.ReadFull(data, p.Frame)
	return p, err
}

func DecodeFull(p []byte, maxPacketSize int) ([]*Packet, error) {
	var packets []*Packet
	psize := len(p)
	for {
		if psize <= 0 {
			break
		}
		maxSize := min(psize, maxPacketSize)
		pkt, err := Decode(bytes.NewBuffer(p[:maxSize]))
		if err != nil {
			return nil, err
		}

		packets = append(packets, pkt)
		psize -= maxSize
		p = p[maxSize:]
	}
	if len(packets) == 0 {
		return nil, fmt.Errorf("unable to decode packets")
	}
	return packets, nil
}

// CopyBuffer re-frames the TDS stream from src onto dst, issuing exactly one
// dst.Write per TDS packet.
//
// dst is a packet-stream writer whose Write boundaries become hoop packet
// boundaries, so this cannot be an io.Copy: the agent-side proxy decodes
// packets from the buffer it is handed, and arbitrary TCP chunking would
// desync it.
//
// Unlike DecodeFull, which slices a buffer by a caller-supplied maximum packet
// size, this honours each packet's own length header — the only framing the
// sender actually guarantees.
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
		if _, err := dst.Write(pkt.Encode()); err != nil {
			return err
		}
	}
}
