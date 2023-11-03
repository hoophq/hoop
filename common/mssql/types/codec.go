package types

import (
	"bytes"
	"fmt"
	"io"
)

func Decode(data io.Reader) (*Packet, error) {
	p := &Packet{}
	_, err := io.ReadFull(data, p.header[:])
	if err != nil {
		return nil, err
	}
	if _, ok := packetTypeMap[PacketType(p.header[0])]; !ok {
		return nil, fmt.Errorf("decoded an unknown packet type [%X]", p.header[0])
	}
	pktLen := p.Length() - 8
	p.Frame = make([]byte, pktLen)
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
