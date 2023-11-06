package mssql

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	mssqltypes "github.com/runopsio/hoop/common/mssql/types"
)

// this connection is used during TLS Handshake
// TDS protocol requires TLS handshake messages to be sent inside TDS packets
// See: https://github.com/denisenkom/go-mssqldb/blob/master/net.go
type tlsHandshakeConn struct {
	c        net.Conn
	pktLen   uint16
	upgraded bool
}

// Read pre login responses when performing the tls handshake based on
// the packet header size. After the connection is upgraded,
// packets will will be read without further modification.
func (c *tlsHandshakeConn) Read(b []byte) (n int, err error) {
	if c.upgraded {
		n, err = c.c.Read(b)
		return
	}
	// starting reading the packet header
	if c.pktLen == 0 {
		var header [8]byte
		if _, err := c.c.Read(header[:]); err != nil {
			return 0, fmt.Errorf("failed reading packet header, err=%v", err)
		}
		if header[0] != byte(mssqltypes.PacketPreloginType) {
			return 0, fmt.Errorf("unexpected packet, header=% X", header)
		}
		c.pktLen = binary.BigEndian.Uint16(header[2:4]) - 8
	}
	// finishes reading and reset the packet header counter moving
	// to the next packet.
	if len(b) > int(c.pktLen) {
		n, err = c.c.Read(b[:c.pktLen])
		c.pktLen = 0
		return
	}
	// read it and decrease the amount
	n, err = c.c.Read(b)
	if err == nil {
		c.pktLen = uint16(c.pktLen - uint16(n))
	}
	return
}

// Write pre login packets when performing the tls handshake.
// After the connection is upgraded, packets will be wrote
// with its headers.
func (c *tlsHandshakeConn) Write(b []byte) (n int, err error) {
	if c.upgraded {
		return c.c.Write(b)
	}
	header := newPacketPreLoginHeader(uint16(len(b)))
	data := append(header[:], b...)
	return c.c.Write(data)
}

func (c *tlsHandshakeConn) Close() error                       { return nil }
func (c *tlsHandshakeConn) LocalAddr() net.Addr                { return nil }
func (c *tlsHandshakeConn) RemoteAddr() net.Addr               { return nil }
func (c *tlsHandshakeConn) SetDeadline(_ time.Time) error      { return nil }
func (c *tlsHandshakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *tlsHandshakeConn) SetWriteDeadline(_ time.Time) error { return nil }

// newPacketPreLoginHeader creates a packet header based on the
// provided size. For now it's safe to rely on those hard-coded values.
func newPacketPreLoginHeader(headerSize uint16) [8]byte {
	var header [8]byte
	header[0] = byte(mssqltypes.PacketPreloginType)

	// status
	header[1] = 0x01
	// length
	binary.BigEndian.PutUint16(header[2:4], headerSize+8)

	// spid
	header[4] = 0x00
	header[5] = 0x00

	// id
	header[6] = 0x01
	// window
	header[7] = 0x00
	return header
}
