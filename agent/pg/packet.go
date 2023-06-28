package pg

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/runopsio/hoop/common/pg"
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

func (p *Packet) EncodeAsReader() *bufio.Reader {
	return bufio.NewReader(bytes.NewBuffer(p.Encode()))
}

// HeaderLength return the packet length (frame) including itself (header)
func (p *Packet) HeaderLength() int {
	pktLen := binary.BigEndian.Uint32(p.header[:])
	return int(pktLen)
}

// Length returns the packet header length + type
func (p *Packet) Length() int { return p.HeaderLength() + 1 }

func (p *Packet) Type() pg.PacketType {
	return pg.PacketType(*p.typ)
}

func (p *Packet) Frame() []byte {
	return p.frame
}

func (p *Packet) Dump() {
	fmt.Print(hex.Dump(p.Encode()))
}

func (p *Packet) IsFrontendSSLRequest() bool {
	if len(p.frame) == 4 {
		v := make([]byte, 4)
		_ = copy(v, p.frame)
		sslRequest := binary.BigEndian.Uint32(v)
		return sslRequest == pg.ClientSSLRequestMessage ||
			sslRequest == pg.ClientGSSENCRequestMessage
	}
	return false
}

func (p *Packet) IsCancelRequest() bool {
	if len(p.frame) > 4 {
		v := make([]byte, 4)
		_ = copy(v, p.frame[:4])
		cancelRequest := binary.BigEndian.Uint32(v)
		return cancelRequest == pg.ClientCancelRequestMessage
	}
	return false
}

func NewReader(rd io.Reader) Reader {
	return bufio.NewReader(rd)
}

func NewPacketWithType(t pg.PacketType) *Packet {
	typ := byte(t)
	return &Packet{typ: &typ}
}

func DecodeTypedPacket(r Reader) (int, *Packet, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	p := &Packet{typ: &typ}
	nread, err := io.ReadFull(r, p.header[:])
	if err != nil {
		return nread, nil, err
	}
	pktLen := p.HeaderLength() - 4 // length includes itself.
	if pktLen > pg.DefaultBufferSize || pktLen < 0 {
		return nread, nil, fmt.Errorf("max size reached")
	}
	p.frame = make([]byte, pktLen)
	n, err := io.ReadFull(r, p.frame)
	if err != nil {
		return n, nil, err
	}
	return nread + n, p, nil
}

func DecodeStartupPacket(startupPacket Reader) (int, *Packet, error) {
	p := &Packet{typ: nil}
	nread, err := io.ReadFull(startupPacket, p.header[:])
	if err != nil {
		return nread, nil, err
	}
	pktLen := p.HeaderLength() - 4 // length includes itself.
	if pktLen > pg.DefaultBufferSize || pktLen < 0 {
		return nread, nil, fmt.Errorf("max size reached")
	}
	p.frame = make([]byte, pktLen)
	n, err := io.ReadFull(startupPacket, p.frame)
	if err != nil {
		return 0, nil, fmt.Errorf("failed reading packet frame, err=%v", err)
	}
	return nread + n, p, err
}

func DecodeStartupPacketWithUsername(startupPacket Reader, pgUsername string) (*Packet, error) {
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

func NewSASLInitialResponsePacket(authData []byte) *Packet {
	p := NewPacketWithType(pg.ClientPassword)
	p.frame = append(p.frame, "SCRAM-SHA-256"...)
	p.frame = append(p.frame, byte(0))
	authLength := make([]byte, 4)
	binary.BigEndian.PutUint32(authLength, uint32(len(authData)))
	p.frame = append(p.frame, authLength...)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewSASLResponse(authData []byte) *Packet {
	return NewPacketWithType(pg.ClientPassword).
		setFrame(authData).
		setHeaderLength(len(authData) + 4)
}

func NewPasswordMessage(authData []byte) *Packet {
	p := NewPacketWithType(pg.ClientPassword)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewAuthenticationOK() *Packet {
	var okPacket [4]byte
	return NewPacketWithType(pg.ServerAuth).
		setFrame(okPacket[:]).
		setHeaderLength(8)
}

func NewDataRowPacket(fieldCount uint16, dataRowValues ...string) *Packet {
	typ := pg.ServerDataRow.Byte()
	p := &Packet{typ: &typ}
	var fieldCountBytes [2]byte
	binary.BigEndian.PutUint16(fieldCountBytes[:], fieldCount)
	p.frame = append(p.frame, fieldCountBytes[:]...)
	for _, val := range dataRowValues {
		var columnLen [4]byte
		binary.BigEndian.PutUint32(columnLen[:], uint32(len(val)))
		p.frame = append(p.frame, columnLen[:]...)
		p.frame = append(p.frame, []byte(val)...)
	}
	return p.setHeaderLength(len(p.frame) + 4)
}

// https://www.postgresql.org/docs/current/protocol-error-fields.html
func NewErrorPacketResponse(msg string, sev pg.Severity, errCode pg.Code) []*Packet {
	p := NewPacketWithType(pg.ServerErrorResponse)
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
	readyPkt := NewPacketWithType(pg.ServerReadyForQuery).
		setFrame([]byte{pg.ServerIdle}).
		setHeaderLength(1 + 4)
	return []*Packet{p, readyPkt}
}

func NewFatalError(msg string) *Packet {
	p := NewPacketWithType(pg.ServerErrorResponse)
	// Severity: ERROR, FATAL, INFO, etc
	p.frame = append(p.frame, 'S')
	p.frame = append(p.frame, pg.LevelFatal...)
	p.frame = append(p.frame, '\000')
	p.frame = append(p.frame, 'V')
	p.frame = append(p.frame, pg.LevelFatal...)
	p.frame = append(p.frame, '\000')
	// the SQLSTATE code for the error
	p.frame = append(p.frame, 'C')
	p.frame = append(p.frame, pg.ConnectionFailure...)
	p.frame = append(p.frame, '\000')
	// Message: the primary human-readable error message.
	// This should be accurate but terse (typically one line).
	p.frame = append(p.frame, 'M')
	p.frame = append(p.frame, msg...)
	p.frame = append(p.frame, '\000', '\000')
	return p.setHeaderLength(len(p.frame) + 4)
}
