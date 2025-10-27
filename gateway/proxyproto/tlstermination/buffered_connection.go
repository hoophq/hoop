package tlstermination

import (
	"bufio"
	"net"
	"time"
)

var _ BufferedConnection = (*bufferedConnection)(nil)

// peekWaitDuration is the maximum time to wait for peeking into the connection
// this avoids waiting forever on postgres/tls handshake checks if there isn't enough
// bytes in the FIFO
const peekWaitDuration = time.Second

// BufferedConnection is a net.Conn with a Peek method
type BufferedConnection interface {
	net.Conn
	// Peek allows peeking into the connection without consuming the bytes
	// Subsequent calls to Read will still return the peeked bytes
	Peek(int) ([]byte, error)
}

// bufferedConnection is a net.Conn with a buffered reader
// allows peeking into the connection without consuming the bytes
type bufferedConnection struct {
	net.Conn
	r *bufio.Reader
}

// NewBufferedConnection wraps a net.Conn with a buffered reader
func NewBufferedConnection(conn net.Conn) BufferedConnection {
	return &bufferedConnection{
		Conn: conn,
		r:    bufio.NewReader(conn),
	}
}

func (b *bufferedConnection) Peek(n int) ([]byte, error) {
	// set a short deadline for peeking
	// This avoids waiting forever if there is not enough bytes in the buffer
	_ = b.Conn.SetReadDeadline(time.Now().Add(peekWaitDuration))
	d, err := b.r.Peek(n)
	_ = b.Conn.SetReadDeadline(time.Time{}) // clear the deadline
	return d, err
}

func (b *bufferedConnection) Read(p []byte) (int, error) {
	return b.r.Read(p)
}
