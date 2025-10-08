package tlstermination

import (
	"bufio"
	"net"
)

var _ BufferedConnection = (*bufferedConnection)(nil)

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
	return b.r.Peek(n)
}

func (b *bufferedConnection) Read(p []byte) (int, error) {
	return b.r.Read(p)
}
