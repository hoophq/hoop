package pgtypes

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

type Packet struct {
	typ    *byte
	header [4]byte
	frame  []byte
}

func (p *Packet) Encode() []byte {
	dst := make([]byte, p.HeaderLength())
	_ = copy(dst, append(p.header[:], p.frame...))
	if p.typ != nil {
		dst = append([]byte{*p.typ}, dst...)
	}
	return dst
}

// HeaderLength return the packet length (frame) including itself (header)
func (p *Packet) HeaderLength() int {
	pktLen := binary.BigEndian.Uint32(p.header[:])
	return int(pktLen)
}

func (p *Packet) Type() (b PacketType) {
	if p.typ != nil {
		return PacketType(*p.typ)
	}
	return
}

// Length returns the packet header length + type
func (p *Packet) Length() int   { return p.HeaderLength() + 1 }
func (p *Packet) Frame() []byte { return p.frame }
func (p *Packet) Dump()         { fmt.Print(hex.Dump(p.Encode())) }

func (p *Packet) IsCancelRequest() bool {
	if len(p.frame) > 4 {
		v := make([]byte, 4)
		_ = copy(v, p.frame[:4])
		cancelRequest := binary.BigEndian.Uint32(v)
		return cancelRequest == ClientCancelRequestMessage
	}
	return false
}

func Decode(data io.Reader) (*Packet, error) {
	typ := make([]byte, 1)
	_, err := data.Read(typ)
	if err != nil {
		return nil, err
	}
	pkt := &Packet{typ: nil}
	if !isClientType(typ[0]) {
		pkt.header[0] = typ[0]
		if _, err := io.ReadFull(data, pkt.header[1:]); err != nil {
			return nil, err
		}
		pktLen := pkt.HeaderLength() - 4 // length includes itself.
		if pktLen > DefaultBufferSize || pktLen < 0 {
			return nil, fmt.Errorf("max size (%v) reached", DefaultBufferSize)
		}
		pkt.frame = make([]byte, pktLen)
		_, err := io.ReadFull(data, pkt.frame)
		if err != nil {
			return nil, fmt.Errorf("failed reading packet frame, err=%v", err)
		}
		return pkt, err
	}
	pkt = &Packet{typ: &typ[0]}
	if _, err := io.ReadFull(data, pkt.header[:]); err != nil {
		return nil, err
	}
	pktLen := pkt.HeaderLength() - 4 // length includes itself.
	if pktLen > DefaultBufferSize || pktLen < 0 {
		return nil, fmt.Errorf("max size (%v) reached", DefaultBufferSize)
	}
	pkt.frame = make([]byte, pktLen)
	if _, err := io.ReadFull(data, pkt.frame); err != nil {
		return nil, err
	}
	return pkt, nil
}

func SimpleQueryContent(payload []byte) (bool, []byte, error) {
	r := bufio.NewReaderSize(bytes.NewBuffer(payload), DefaultBufferSize)
	typ, err := r.ReadByte()
	if err != nil {
		return false, nil, fmt.Errorf("failed reading first byte: %v", err)
	}
	switch PacketType(typ) {
	case ClientSimpleQuery:
		break
	case ClientParse:
		return false, nil, fmt.Errorf("extended query protocol is not supported")
	default:
		return false, nil, nil
	}

	header := [4]byte{}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return false, nil, fmt.Errorf("failed reading packet header, err=%v", err)
	}
	pktLen := binary.BigEndian.Uint32(header[:]) - 4 // don't include header size (4)
	if uint32(len(payload[5:])) != pktLen {
		return false, nil, fmt.Errorf("unexpected packet payload, received %v/%v", len(payload[5:]), pktLen)
	}
	queryFrame := make([]byte, pktLen)
	if _, err := io.ReadFull(r, queryFrame); err != nil {
		return false, nil, fmt.Errorf("failed reading simple query, err=%v", err)
	}
	return true, queryFrame, nil
}

type BackendKeyData struct {
	Pid       uint32
	SecretKey uint32
}
