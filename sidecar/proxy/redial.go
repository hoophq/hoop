package proxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// redialConn is an http lane's upstream. When the upstream closes the
// connection before either side sent a byte, the next Write dials a new one,
// so a slow verdict does not cost the client its request.
type redialConn struct {
	dial func() (net.Conn, error)

	mu           sync.Mutex
	conn         net.Conn
	used         bool
	lost         bool
	closed       bool
	swapped      chan struct{}
	readDeadline time.Time
}

func newRedialConn(conn net.Conn, dial func() (net.Conn, error)) *redialConn {
	return &redialConn{conn: conn, dial: dial, swapped: make(chan struct{})}
}

func (r *redialConn) Read(b []byte) (int, error) {
	for {
		r.mu.Lock()
		c, lost, closed, swapped := r.conn, r.lost, r.closed, r.swapped
		r.mu.Unlock()
		if closed {
			return 0, net.ErrClosed
		}
		if lost {
			<-swapped
			continue
		}
		n, err := c.Read(b)
		if n > 0 {
			r.mu.Lock()
			r.used = true
			r.mu.Unlock()
		}
		if n > 0 || err == nil || !closedByPeer(err) {
			return n, err
		}
		r.mu.Lock()
		if r.used || r.closed || r.conn != c {
			r.mu.Unlock()
			return n, err
		}
		r.lost = true
		r.mu.Unlock()
		_ = c.Close()
	}
}

// Write is called by one goroutine, the client pump.
func (r *redialConn) Write(b []byte) (int, error) {
	r.mu.Lock()
	c, lost, closed := r.conn, r.lost, r.closed
	if !lost {
		r.used = true
	}
	r.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	if lost {
		nc, err := r.dial()
		if err != nil {
			return 0, err
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			_ = nc.Close()
			return 0, net.ErrClosed
		}
		if !r.readDeadline.IsZero() {
			_ = nc.SetReadDeadline(r.readDeadline)
		}
		r.conn, r.lost, r.used = nc, false, true
		close(r.swapped)
		r.swapped = make(chan struct{})
		r.mu.Unlock()
		c = nc
	}
	return c.Write(b)
}

func (r *redialConn) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	c := r.conn
	close(r.swapped)
	r.mu.Unlock()
	return c.Close()
}

func (r *redialConn) current() net.Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conn
}

func (r *redialConn) LocalAddr() net.Addr  { return r.current().LocalAddr() }
func (r *redialConn) RemoteAddr() net.Addr { return r.current().RemoteAddr() }

func (r *redialConn) SetDeadline(t time.Time) error {
	r.mu.Lock()
	r.readDeadline = t
	c := r.conn
	r.mu.Unlock()
	return c.SetDeadline(t)
}

func (r *redialConn) SetReadDeadline(t time.Time) error {
	r.mu.Lock()
	r.readDeadline = t
	c := r.conn
	r.mu.Unlock()
	return c.SetReadDeadline(t)
}

func (r *redialConn) SetWriteDeadline(t time.Time) error { return r.current().SetWriteDeadline(t) }

func closedByPeer(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}
