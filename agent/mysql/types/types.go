package types

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/runopsio/hoop/common/log"
)

func (r ClientFlag) Has(flag ClientFlag) bool {
	return r&flag != 0
}

func (r *ClientFlag) Set(flag ClientFlag) *ClientFlag {
	*r |= flag
	return r
}

func (r *ClientFlag) Unset(flag ClientFlag) *ClientFlag {
	if r.Has(flag) {
		*r = *r - flag
	}
	return r
}

func (r *ClientFlag) String() string {
	var names []string
	for i := uint64(1); i <= uint64(1)<<31; i = i << 1 {
		name, ok := flagsMap[ClientFlag(i)]
		if ok {
			isSet := 0
			if r.Has(ClientFlag(i)) {
				isSet = 1
			}
			names = append(names, fmt.Sprintf("0x%08x set=%v %s", i, isSet, name))
		}
	}
	return strings.Join(names, ", ")
}

type SourceType int

const (
	SourceServer SourceType = 1 << iota
	SourceClient
)

func (o SourceType) String() string {
	switch o {
	case SourceServer:
		return "server"
	case SourceClient:
		return "client"
	}
	return "unknown"
}

// Packet represents a MySQL packet
// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_basic_packets.html
type Packet struct {
	// Payload of the packet
	Frame []byte
	// Payload []byte
	// sequenceID is incremented with each packet and may wrap around.
	// It starts at 0 and is reset to 0 when a new command begins in the Command Phase.
	sequenceID byte

	// Source indicates the origin of the packet (client or server)
	Source SourceType
}

func (p *Packet) SetSeq(v uint8) {
	p.sequenceID = v
}

func (p *Packet) Seq() uint8 {
	return p.sequenceID
}

func (p *Packet) Encode() []byte {
	var pktLen [4]byte
	binary.LittleEndian.PutUint32(pktLen[:], uint32(len(p.Frame)))
	return append(
		append(pktLen[:3], p.sequenceID),
		p.Frame...)
}

// Dump the packet as hex encoded without log formating
// if the default logger is debug
func (p *Packet) Dump() {
	if log.IsDebugLevel {
		fmt.Println(hex.Dump(p.Encode()))
	}
}

// errMySQL is an error type which represents a single MySQL error
type errMySQL struct {
	pkt *Packet
	msg string
}

func (me *errMySQL) Error() string { return me.msg }

func NewErrPacket(errno uint16, sqlState, msg string) error {
	msg = fmt.Sprintf("[hoopagent] %v", msg)
	payloadLen := len(msg) + 1 + 2 + 6
	if len(sqlState) == 0 {
		sqlState = `HY000` // generic error
	}
	payload := make([]byte, payloadLen)
	payload[0] = 0xff
	binary.LittleEndian.PutUint16(payload[1:3], uint16(errno))
	// SQL State [optional: # + 5bytes string]
	payload[3] = 0x23
	copy(payload[4:9], sqlState[:])
	copy(payload[9:], msg)
	return &errMySQL{pkt: &Packet{Frame: payload}, msg: msg}
}

func EncodeErrPacket(err error, seq uint8) []byte {
	if me, ok := err.(*errMySQL); ok {
		me.pkt.sequenceID = seq
		return me.pkt.Encode()
	}
	// send a generic error
	if err, _ := NewErrPacket(6000, "", err.Error()).(*errMySQL); err != nil {
		err.pkt.sequenceID = seq
		return err.pkt.Encode()
	}
	return nil
}

func NewPacket(payload []byte, seq uint8) *Packet {
	return &Packet{Frame: payload, sequenceID: seq}
}
