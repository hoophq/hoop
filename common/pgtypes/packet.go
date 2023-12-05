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

func (p *Packet) setFrame(frame []byte) *Packet {
	p.frame = frame
	return p
}

func (p *Packet) setHeaderLength(length int) *Packet {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(length))
	p.header = header
	return p
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

// Length returns the packet header length + type
func (p *Packet) Length() int { return p.HeaderLength() + 1 }

func (p *Packet) Type() (b PacketType) {
	if p.typ != nil {
		return PacketType(*p.typ)
	}
	return
}

func (p *Packet) Frame() []byte { return p.frame }
func (p *Packet) Dump()         { fmt.Print(hex.Dump(p.Encode())) }

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

func (p *Packet) IsCancelRequest() bool {
	if len(p.frame) > 4 {
		v := make([]byte, 4)
		_ = copy(v, p.frame[:4])
		cancelRequest := binary.BigEndian.Uint32(v)
		return cancelRequest == ClientCancelRequestMessage
	}
	return false
}

func NewPacketWithType(t PacketType) *Packet {
	typ := byte(t)
	return &Packet{typ: &typ}
}

func DecodeTypedPacket(r io.Reader) (int, *Packet, error) {
	typ := make([]byte, 1)
	read, err := r.Read(typ)
	if err != nil {
		return 0, nil, err
	}
	p := &Packet{typ: &typ[0]}
	nread, err := io.ReadFull(r, p.header[:])
	if err != nil {
		return nread, nil, err
	}
	pktLen := p.HeaderLength() - 4 // length includes itself.
	if pktLen > DefaultBufferSize || pktLen < 0 {
		return nread, nil, fmt.Errorf("max size (%v) reached", DefaultBufferSize)
	}
	p.frame = make([]byte, pktLen)
	n, err := io.ReadFull(r, p.frame)
	if err != nil {
		return n, nil, err
	}
	return read + nread + n, p, nil
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

func DecodeStartupPacket(startupPacket io.Reader) (int, *Packet, error) {
	p := &Packet{typ: nil}
	nread, err := io.ReadFull(startupPacket, p.header[:])
	if err != nil {
		return nread, nil, err
	}
	pktLen := p.HeaderLength() - 4 // length includes itself.
	if pktLen > DefaultBufferSize || pktLen < 0 {
		return nread, nil, fmt.Errorf("max size reached")
	}
	p.frame = make([]byte, pktLen)
	n, err := io.ReadFull(startupPacket, p.frame)
	if err != nil {
		return 0, nil, fmt.Errorf("failed reading packet frame, err=%v", err)
	}
	return nread + n, p, err
}

func SimpleQueryContent(payload []byte) (bool, []byte, error) {
	r := bufio.NewReaderSize(bytes.NewBuffer(payload), DefaultBufferSize)
	typ, err := r.ReadByte()
	if err != nil {
		return false, nil, fmt.Errorf("failed reading first byte: %v", err)
	}
	if PacketType(typ) != ClientSimpleQuery {
		return false, nil, nil
	}

	header := [4]byte{}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return true, nil, fmt.Errorf("failed reading header, err=%v", err)
	}
	pktLen := binary.BigEndian.Uint32(header[:]) - 4 // don't include header size (4)
	if uint32(len(payload[5:])) != pktLen {
		return true, nil, fmt.Errorf("unexpected packet payload, received %v/%v", len(payload[5:]), pktLen)
	}
	queryFrame := make([]byte, pktLen)
	if _, err := io.ReadFull(r, queryFrame); err != nil {
		return true, nil, fmt.Errorf("failed reading query, err=%v", err)
	}
	return true, queryFrame, nil
}

func DecodeStartupPacketWithUsername(startupPacket io.Reader, pgUsername string) (*Packet, error) {
	_, decPkt, err := DecodeStartupPacket(startupPacket)
	if err != nil {
		return decPkt, err
	}
	// protocol version + user name string
	pos := 4 + 5
	if len(decPkt.frame) <= pos {
		return nil, fmt.Errorf("it's not a startup packet")
	}
	usridx := bytes.IndexByte(decPkt.frame[pos:], 0x00)
	if usridx == -1 {
		return nil, fmt.Errorf("startup packet doesn't have user attribute")
	}
	pktFrame := bytes.Replace(
		decPkt.frame,
		decPkt.frame[pos:pos+usridx],
		[]byte(pgUsername),
		1)
	decPkt.frame = pktFrame
	pktLen := len(decPkt.frame) + 4
	return decPkt.setHeaderLength(pktLen), nil
}

func NewSSLRequestPacket() [8]byte {
	var packet [8]byte
	binary.BigEndian.PutUint32(packet[0:4], 8)
	binary.BigEndian.PutUint32(packet[4:8], ClientSSLRequestMessage)
	return packet
}

func NewSASLInitialResponsePacket(authData []byte) *Packet {
	p := NewPacketWithType(ClientPassword)
	p.frame = append(p.frame, "SCRAM-SHA-256"...)
	p.frame = append(p.frame, byte(0))
	authLength := make([]byte, 4)
	binary.BigEndian.PutUint32(authLength, uint32(len(authData)))
	p.frame = append(p.frame, authLength...)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewSASLResponse(authData []byte) *Packet {
	return NewPacketWithType(ClientPassword).
		setFrame(authData).
		setHeaderLength(len(authData) + 4)
}

func NewPasswordMessage(authData []byte) *Packet {
	p := NewPacketWithType(ClientPassword)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewAuthenticationOK() *Packet {
	var okPacket [4]byte
	return NewPacketWithType(ServerAuth).
		setFrame(okPacket[:]).
		setHeaderLength(8)
}

func NewDataRowPacket(fieldCount uint16, dataRowValues ...string) *Packet {
	typ := ServerDataRow.Byte()
	p := &Packet{typ: &typ}
	var fieldCountBytes [2]byte
	binary.BigEndian.PutUint16(fieldCountBytes[:], fieldCount)
	p.frame = append(p.frame, fieldCountBytes[:]...)
	for _, val := range dataRowValues {
		var columnLen [4]byte
		if val == DLPColumnNullType {
			binary.BigEndian.PutUint32(columnLen[:], ServerDataRowNull)
			p.frame = append(p.frame, columnLen[:]...)
			continue
		}
		binary.BigEndian.PutUint32(columnLen[:], uint32(len(val)))
		p.frame = append(p.frame, columnLen[:]...)
		p.frame = append(p.frame, []byte(val)...)
	}
	return p.setHeaderLength(len(p.frame) + 4)
}

// https://www.postgresql.org/docs/current/protocol-error-fields.html
func NewErrorPacketResponse(msg string, sev Severity, errCode Code) []*Packet {
	p := NewPacketWithType(ServerErrorResponse)
	// Severity: ERROR, FATAL, INFO, etc
	p.frame = append(p.frame, 'S')
	p.frame = append(p.frame, sev...)
	p.frame = append(p.frame, '\000')
	p.frame = append(p.frame, 'V')
	p.frame = append(p.frame, sev...)
	p.frame = append(p.frame, '\000')
	// the SQLSTATE code for the error
	p.frame = append(p.frame, 'C')
	p.frame = append(p.frame, errCode...)
	p.frame = append(p.frame, '\000')
	// Message: the primary human-readable error message.
	// This should be accurate but terse (typically one line).
	p.frame = append(p.frame, 'M')
	p.frame = append(p.frame, msg...)
	p.frame = append(p.frame, '\000', '\000')

	p.setHeaderLength(len(p.frame) + 4)
	readyPkt := NewPacketWithType(ServerReadyForQuery).
		setFrame([]byte{ServerIdle}).
		setHeaderLength(1 + 4)
	return []*Packet{p, readyPkt}
}

func NewFatalError(msg string) *Packet {
	p := NewPacketWithType(ServerErrorResponse)
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
	p.frame = append(p.frame, msg...)
	p.frame = append(p.frame, '\000', '\000')
	return p.setHeaderLength(len(p.frame) + 4)
}
