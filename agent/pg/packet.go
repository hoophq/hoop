package pg

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/runopsio/hoop/common/pg"
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
	dst := make([]byte, p.Length())
	_ = copy(dst, append(p.header[:], p.frame...))
	if p.typ != nil {
		dst = append([]byte{*p.typ}, dst...)
	}
	return dst
}

func (p *Packet) EncodeAsReader() *bufio.Reader {
	return bufio.NewReader(bytes.NewBuffer(p.Encode()))
}

// Length return the packet length (frame) including itself (header)
func (p *Packet) Length() int {
	pktLen := binary.BigEndian.Uint32(p.header[:])
	return int(pktLen)
}

func (p *Packet) Type() pg.PacketType {
	return pg.PacketType(*p.typ)
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
	return bufio.NewReaderSize(rd, pg.DefaultBufferSize)
}

func newPacketWithType(t pg.PacketType) *Packet {
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
	pktLen := p.Length() - 4 // length includes itself.
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
	pktLen := p.Length() - 4 // length includes itself.
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
	p := newPacketWithType(pg.ClientPassword)
	p.frame = append(p.frame, "SCRAM-SHA-256"...)
	p.frame = append(p.frame, byte(0))
	authLength := make([]byte, 4)
	binary.BigEndian.PutUint32(authLength, uint32(len(authData)))
	p.frame = append(p.frame, authLength...)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewSASLResponse(authData []byte) *Packet {
	return newPacketWithType(pg.ClientPassword).
		setFrame(authData).
		setHeaderLength(len(authData) + 4)
}

func NewPasswordMessage(authData []byte) *Packet {
	p := newPacketWithType(pg.ClientPassword)
	p.frame = append(p.frame, authData...)
	return p.setHeaderLength(len(p.frame) + 4)
}

func NewAuthenticationOK() *Packet {
	var okPacket [4]byte
	return newPacketWithType(pg.ServerAuth).
		setFrame(okPacket[:]).
		setHeaderLength(8)
}

// https://www.postgresql.org/docs/current/protocol-error-fields.html
func NewErrorPacketResponse(msg string, sev pg.Severity, errCode pg.Code) []*Packet {
	p := newPacketWithType(pg.ServerErrorResponse)
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
	readyPkt := newPacketWithType(pg.ServerReadyForQuery).
		setFrame([]byte{pg.ServerIdle}).
		setHeaderLength(1 + 4)
	return []*Packet{p, readyPkt}
}

func NewFatalError(msg string) *Packet {
	p := newPacketWithType(pg.ServerErrorResponse)
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
