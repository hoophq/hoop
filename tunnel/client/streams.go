package client

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/hoophq/hoop/tunnel/wire"
)

// Stream is a single full-duplex TCP-like channel multiplexed inside a
// Session. It satisfies io.ReadWriteCloser so callers can hand it to
// io.Copy and standard helpers.
//
// Read returns io.EOF on a clean peer close and the stream's error on an
// abnormal close. Write returns the session's error after the stream or
// session is closed.
type Stream struct {
	sess *Session
	id   uint32
	name string

	// readBuf carries inbound bytes, posted by Session.dispatch and consumed
	// by Read. It is unbounded; flow control is left to TCP at the peer's
	// netstack — we just buffer in RAM, which matches the gateway's existing
	// per-connection memory profile.
	mu      sync.Mutex
	cond    *sync.Cond
	buf     [][]byte // FIFO of received payloads
	readErr error    // set on close/error, returned by Read once buf is drained
	closed  bool     // true after teardown

	// writeClosed is set once we have sent a FrameTypeStreamClose. After
	// that, Write returns io.ErrClosedPipe.
	writeClosed bool
}

func newStream(sess *Session, id uint32, name string) *Stream {
	s := &Stream{sess: sess, id: id, name: name}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// ID returns the wire stream id. Exposed for diagnostics and tests.
func (s *Stream) ID() uint32 { return s.id }

// Name returns the connection name this stream is bound to.
func (s *Stream) Name() string { return s.name }

// Read implements io.Reader.
//
// It blocks until at least one byte is available, the peer closes the
// stream, or the stream is torn down. The current implementation does not
// honour a deadline; the spike doesn't need it. If a caller needs to abort a
// blocked Read, closing the Stream wakes it up.
func (s *Stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && s.readErr == nil {
		s.cond.Wait()
	}
	if len(s.buf) == 0 {
		// readErr is non-nil here. If it's a clean close, return io.EOF; if
		// it's an abnormal close, return the underlying error.
		return 0, s.readErr
	}
	chunk := s.buf[0]
	n := copy(p, chunk)
	if n < len(chunk) {
		s.buf[0] = chunk[n:]
	} else {
		s.buf = s.buf[1:]
	}
	return n, nil
}

// Write implements io.Writer.
//
// Each Write emits exactly one FrameTypeStreamData frame. There is no
// internal buffering; small writes from a caller become small frames on the
// wire. Callers that care about throughput should wrap the Stream in a
// bufio.Writer.
func (s *Stream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	if s.writeClosed || s.closed {
		s.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	s.mu.Unlock()

	// We slice large writes into MaxFrameSize-sized chunks. Most callers
	// (DB clients) write small frames; this is defensive.
	const maxPayload = wire.MaxFrameSize - 256 // header overhead headroom

	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxPayload {
			chunk = chunk[:maxPayload]
		}
		if err := s.sess.writeFrame(wire.Frame{
			Type:     wire.FrameTypeStreamData,
			StreamID: s.id,
			Payload:  chunk,
		}); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// CloseWrite signals that the local side will not send more data on this
// stream. The peer may continue sending until it too closes. This maps to a
// half-close in TCP terms (FIN from us; ACK then FIN later from them).
//
// After CloseWrite, Read still returns the rest of the inbound data.
func (s *Stream) CloseWrite() error {
	s.mu.Lock()
	if s.writeClosed {
		s.mu.Unlock()
		return nil
	}
	s.writeClosed = true
	s.mu.Unlock()
	return s.sess.writeFrame(wire.Frame{
		Type:     wire.FrameTypeStreamClose,
		StreamID: s.id,
	})
}

// Close tears the stream down on both sides. It is safe to call multiple
// times.
func (s *Stream) Close() error {
	err := s.CloseWrite()
	s.teardown(io.ErrClosedPipe)
	s.sess.removeStream(s.id)
	return err
}

// SetReadDeadline is a placeholder for the net.Conn-like interface gVisor
// expects. The spike's blocking Read does not honour it; we return nil so
// callers that always set a deadline don't error out. Wiring deadlines
// properly is a follow-up.
func (s *Stream) SetReadDeadline(t time.Time) error  { return nil }
func (s *Stream) SetWriteDeadline(t time.Time) error { return nil }
func (s *Stream) SetDeadline(t time.Time) error      { return nil }

// --- methods used by Session.dispatch ---

func (s *Stream) deliverData(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.buf = append(s.buf, p)
	s.cond.Signal()
}

func (s *Stream) deliverClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr == nil {
		s.readErr = io.EOF
	}
	s.cond.Broadcast()
}

func (s *Stream) deliverError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr == nil {
		if err == nil {
			err = errors.New("tunnel: peer stream error")
		}
		s.readErr = err
	}
	s.closed = true
	s.cond.Broadcast()
}

func (s *Stream) teardown(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.readErr == nil {
		if cause == nil {
			cause = io.ErrClosedPipe
		}
		s.readErr = cause
	}
	s.cond.Broadcast()
}
