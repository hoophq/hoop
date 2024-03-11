package mongotypes

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

type PacketHeader struct {
	MessageLength uint32
	RequestID     uint32
	ResponseTo    uint32
	OpCode        uint32
}

func (h *PacketHeader) Encode() []byte {
	pktBytes := make([]byte, binary.Size(h))
	binary.LittleEndian.PutUint32(pktBytes[0:4], h.MessageLength)
	binary.LittleEndian.PutUint32(pktBytes[4:8], h.RequestID)
	binary.LittleEndian.PutUint32(pktBytes[8:12], h.ResponseTo)
	binary.LittleEndian.PutUint32(pktBytes[12:16], h.OpCode)
	return pktBytes
}

type Packet struct {
	MessageLength uint32
	RequestID     uint32
	ResponseTo    uint32
	OpCode        uint32

	Frame []byte
}

func (p *Packet) Encode() []byte {
	pktBytes := make([]byte, len(p.Frame)+16)
	binary.LittleEndian.PutUint32(pktBytes[0:4], p.MessageLength)
	binary.LittleEndian.PutUint32(pktBytes[4:8], p.RequestID)
	binary.LittleEndian.PutUint32(pktBytes[8:12], p.ResponseTo)
	binary.LittleEndian.PutUint32(pktBytes[12:16], p.OpCode)
	_ = copy(pktBytes[16:], p.Frame)
	return pktBytes
}

func (p *Packet) Dump() { fmt.Println(hex.Dump(p.Encode())) }

func Decode(r io.Reader) (*Packet, error) {
	var header [16]byte
	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return nil, err
	}

	p := Packet{
		MessageLength: binary.LittleEndian.Uint32(header[0:4]),
		RequestID:     binary.LittleEndian.Uint32(header[4:8]),
		ResponseTo:    binary.LittleEndian.Uint32(header[8:12]),
		OpCode:        binary.LittleEndian.Uint32(header[12:16]),
	}
	pktLen := int(p.MessageLength - 16)
	frame := make([]byte, pktLen)
	_, err = io.ReadFull(r, frame)
	if err != nil {
		return nil, err
	}
	p.Frame = frame
	return &p, nil
}
