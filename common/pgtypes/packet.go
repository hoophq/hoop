package pgtypes

import (
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

type BackendKeyData struct {
	Pid       uint32
	SecretKey uint32
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

func (p *Packet) IsFrontendSSLRequest() bool {
	if len(p.frame) == 4 {
		v := make([]byte, 4)
		_ = copy(v, p.frame)
		sslRequest := binary.BigEndian.Uint32(v)
		return sslRequest == ClientSSLRequestMessage ||
			sslRequest == ClientGSSENCRequestMessage
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

// It parses simple query or extended query format, returns nil in case
// the packet type is not a Query (F) or Parse (F)
func ParseQuery(payload []byte) []byte {
	switch PacketType(payload[0]) {
	case ClientSimpleQuery:
		pktLen := binary.BigEndian.Uint32(payload[1:5])
		return payload[5:pktLen]
	case ClientParse:
		// type (1) + header (4)
		isUnnamedPreparedStmt := payload[5] == 0
		payload := payload[5:]
		if isUnnamedPreparedStmt {
			payload = payload[1:] // remove byte 00
		} else {
			// re-slice to remove the named prepared statement
			idx := bytes.IndexByte(payload, 0x00)
			if idx == -1 {
				return nil
			}
			payload = payload[idx+1:]

		}
		// obtain only the query string statement
		idx := bytes.IndexByte(payload, 0x00)
		if idx == -1 {
			return nil
		}
		return payload[:idx]
	default:
		return nil
	}
}

func NewFatalError(msg string, v ...any) *Packet {
	typ := byte(ServerErrorResponse)
	p := &Packet{typ: &typ}
	// Severity: ERROR, FATAL, INFO, etc
	p.frame = append(p.frame, 'S')
	p.frame = append(p.frame, LevelFatal...)
	p.frame = append(p.frame, '\000')
	p.frame = append(p.frame, 'V')
	p.frame = append(p.frame, LevelFatal...)
	p.frame = append(p.frame, '\000')
	// the SQLSTATE code for the error
	p.frame = append(p.frame, 'C')
	p.frame = append(p.frame, ConnectionFailure...)
	p.frame = append(p.frame, '\000')
	// Message: the primary human-readable error message.
	// This should be accurate but terse (typically one line).
	p.frame = append(p.frame, 'M')
	p.frame = append(p.frame, fmt.Sprintf(msg, v...)...)
	p.frame = append(p.frame, '\000', '\000')
	return p.setHeaderLength(len(p.frame) + 4)
}

func (p *Packet) setHeaderLength(length int) *Packet {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(length))
	p.header = header
	return p
}
