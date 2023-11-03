package types

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

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
	binary.BigEndian.PutUint16(header[2:4], uint16(dataSize)+8)

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
