// Package client implements the tunnel session: a single long-lived
// WebSocket connection to the gateway over which multiple TCP streams are
// multiplexed using the wire framing.
//
// The Session is transport-only. It does not know about the netstack, the
// resolver, or addressing — those layers plug in by calling OpenStream and
// receiving stream-data callbacks. Tests can drive Session directly without
// any of the OS-level plumbing.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/tunnel/wire"
)

// Session owns one WebSocket connection and the streams flowing over it.
//
// A Session is created by Dial, runs until the WebSocket closes or Close is
// called, and is single-use: callers create a new Session to reconnect.
//
// Concurrency: all exported methods are safe to call from any goroutine.
type Session struct {
	conn *websocket.Conn

	// writeMu serializes WebSocket writes. gorilla/websocket requires this:
	// only one goroutine may write at a time.
	writeMu sync.Mutex

	// streamsMu protects the streams map. Read paths take RLock for
	// dispatch; OpenStream and stream teardown take the write lock.
	streamsMu sync.RWMutex
	streams   map[uint32]*Stream
	nextID    atomic.Uint32

	// ctx is the lifetime context for the whole session. Cancelling it tears
	// every stream down and closes the WebSocket.
	ctx    context.Context
	cancel context.CancelCauseFunc

	// closeOnce makes Close idempotent.
	closeOnce sync.Once

	// readErr is the error that ended the read loop (or nil if Close was
	// called first). It is set exactly once.
	readErr atomic.Pointer[error]

	// handler is the callback invoked for peer-initiated streams. nil means
	// inbound opens are rejected. Guarded by handlerMu.
	handlerMu sync.RWMutex
	handler   StreamHandler
}

// DialOptions configures Dial. All fields are optional.
type DialOptions struct {
	// HTTPHeader is applied to the WebSocket upgrade request. Use it for
	// auth headers (Bearer token), correlation IDs, etc.
	HTTPHeader map[string][]string

	// HandshakeTimeout caps how long the WS upgrade may take. Zero means use
	// gorilla/websocket's default (no timeout).
	HandshakeTimeout time.Duration
}

// Dial opens a tunnel session against the given URL. The URL must use ws://
// or wss://. The returned Session is already running its read loop.
func Dial(ctx context.Context, url string, opts DialOptions) (*Session, error) {
	dialer := *websocket.DefaultDialer
	if opts.HandshakeTimeout > 0 {
		dialer.HandshakeTimeout = opts.HandshakeTimeout
	}
	conn, _, err := dialer.DialContext(ctx, url, opts.HTTPHeader)
	if err != nil {
		return nil, fmt.Errorf("tunnel: ws dial: %w", err)
	}
	return newSession(conn), nil
}

// NewServerSession wraps an already-upgraded WebSocket connection on the
// server side. Used by the stub gateway and by tests.
func NewServerSession(conn *websocket.Conn) *Session {
	return newSession(conn)
}

func newSession(conn *websocket.Conn) *Session {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &Session{
		conn:    conn,
		streams: make(map[uint32]*Stream),
		ctx:     ctx,
		cancel:  cancel,
	}
	// Stream IDs start at 1; 0 is reserved for control frames.
	s.nextID.Store(0)
	go s.readLoop()
	return s
}

// Context returns the session's lifetime context. It is cancelled when the
// session ends, with the cause set to the terminating error.
func (s *Session) Context() context.Context { return s.ctx }

// Close shuts the session down. Any in-flight streams are torn down with
// io.ErrClosedPipe.
func (s *Session) Close() error {
	return s.closeWithCause(nil)
}

func (s *Session) closeWithCause(cause error) error {
	var firstErr error
	s.closeOnce.Do(func() {
		if cause == nil {
			cause = errors.New("tunnel: session closed by caller")
		}
		s.cancel(cause)

		// Close every open stream. After this, no goroutine reads/writes the
		// stream map; we can release locks before touching the WebSocket.
		s.streamsMu.Lock()
		streams := s.streams
		s.streams = nil
		s.streamsMu.Unlock()
		for _, st := range streams {
			st.teardown(cause)
		}

		// Best-effort WS close frame, then hard close. We don't wait for the
		// peer's reply — the read loop will see EOF and exit.
		s.writeMu.Lock()
		deadline := time.Now().Add(2 * time.Second)
		_ = s.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			deadline)
		s.writeMu.Unlock()
		firstErr = s.conn.Close()
	})
	return firstErr
}

// StreamHandler is invoked by the server side of a session whenever the peer
// opens a new stream. The handler owns the stream and must Close it when
// done.
//
// On the client side, SetStreamHandler is rarely needed: clients open streams
// outbound. We keep it symmetric so the gateway stub can register a single
// callback.
type StreamHandler func(s *Stream)

// SetStreamHandler installs the callback invoked for inbound StreamOpen
// frames. It must be set before any peer-initiated stream arrives; on the
// client side, leaving it nil causes inbound opens to be rejected with a
// StreamError.
func (s *Session) SetStreamHandler(h StreamHandler) {
	s.handlerMu.Lock()
	s.handler = h
	s.handlerMu.Unlock()
}

// OpenStream initiates a new stream to the named connection. It returns once
// the StreamOpen frame is sent; the gateway acks open by sending the first
// StreamData (or fails it with StreamError). Callers treat the returned
// Stream as a duplex byte pipe.
func (s *Session) OpenStream(name string) (*Stream, error) {
	if name == "" {
		return nil, errors.New("tunnel: OpenStream: empty name")
	}
	if err := s.ctx.Err(); err != nil {
		return nil, fmt.Errorf("tunnel: session closed: %w", context.Cause(s.ctx))
	}
	id := s.allocStreamID()
	st := newStream(s, id, name)

	s.streamsMu.Lock()
	if s.streams == nil {
		s.streamsMu.Unlock()
		return nil, fmt.Errorf("tunnel: session closed: %w", context.Cause(s.ctx))
	}
	s.streams[id] = st
	s.streamsMu.Unlock()

	if err := s.writeFrame(wire.Frame{
		Type:     wire.FrameTypeStreamOpen,
		StreamID: id,
		Name:     name,
	}); err != nil {
		s.removeStream(id)
		return nil, fmt.Errorf("tunnel: write StreamOpen: %w", err)
	}
	return st, nil
}

// allocStreamID returns a fresh, non-zero stream ID. We wrap on overflow but
// that requires 4 billion streams in a single session lifetime — not a real
// concern.
func (s *Session) allocStreamID() uint32 {
	for {
		id := s.nextID.Add(1)
		if id != wire.ControlStreamID {
			return id
		}
	}
}

// writeFrame is the single point of egress to the WebSocket. All frames go
// through here, including control frames.
func (s *Session) writeFrame(f wire.Frame) error {
	buf, err := wire.Encode(f)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, buf)
}

// readLoop reads frames off the WebSocket and dispatches them. It terminates
// the session when the WebSocket returns an error.
func (s *Session) readLoop() {
	defer func() {
		// Whatever the cause, ensure the session is fully torn down.
		errPtr := s.readErr.Load()
		var cause error
		if errPtr != nil {
			cause = *errPtr
		}
		_ = s.closeWithCause(cause)
	}()

	for {
		msgType, buf, err := s.conn.ReadMessage()
		if err != nil {
			e := fmt.Errorf("tunnel: ws read: %w", err)
			s.readErr.Store(&e)
			return
		}
		if msgType != websocket.BinaryMessage {
			// Text or unknown — protocol violation. Drop the session.
			e := fmt.Errorf("tunnel: unexpected ws message type %d", msgType)
			s.readErr.Store(&e)
			return
		}
		f, err := wire.Decode(buf)
		if err != nil {
			e := fmt.Errorf("tunnel: decode frame: %w", err)
			s.readErr.Store(&e)
			return
		}
		s.dispatch(f)
	}
}

// dispatch routes a decoded frame to the right per-stream channel, opens a
// new inbound stream if appropriate, or handles control traffic.
func (s *Session) dispatch(f wire.Frame) {
	switch f.Type {
	case wire.FrameTypePing:
		// Echo back. Best-effort; if writing fails the next op will surface
		// the same error.
		_ = s.writeFrame(wire.Frame{
			Type:     wire.FrameTypePong,
			StreamID: f.StreamID,
			Payload:  f.Payload,
		})
		return
	case wire.FrameTypePong:
		// Liveness ack. The spike doesn't track in-flight pings yet.
		return
	case wire.FrameTypeStreamOpen:
		s.handleInboundOpen(f)
		return
	}

	s.streamsMu.RLock()
	st := s.streams[f.StreamID]
	s.streamsMu.RUnlock()
	if st == nil {
		// Stream gone or never existed. Send a polite error back so the peer
		// stops sending data on a dead stream.
		_ = s.writeFrame(wire.Frame{
			Type:     wire.FrameTypeStreamError,
			StreamID: f.StreamID,
			Payload:  []byte("unknown stream"),
		})
		return
	}
	switch f.Type {
	case wire.FrameTypeStreamData:
		// Copy the payload — the buffer is owned by the read loop and will
		// be reused for the next frame.
		payload := append([]byte(nil), f.Payload...)
		st.deliverData(payload)
	case wire.FrameTypeStreamClose:
		st.deliverClose()
	case wire.FrameTypeStreamError:
		st.deliverError(errors.New(string(f.Payload)))
	default:
		// Unknown frame on a known stream; ignore but don't kill the session.
	}
}

func (s *Session) handleInboundOpen(f wire.Frame) {
	s.handlerMu.RLock()
	h := s.handler
	s.handlerMu.RUnlock()
	if h == nil {
		_ = s.writeFrame(wire.Frame{
			Type:     wire.FrameTypeStreamError,
			StreamID: f.StreamID,
			Payload:  []byte("no inbound stream handler"),
		})
		return
	}
	st := newStream(s, f.StreamID, f.Name)
	s.streamsMu.Lock()
	if s.streams == nil {
		s.streamsMu.Unlock()
		return
	}
	if _, exists := s.streams[f.StreamID]; exists {
		s.streamsMu.Unlock()
		_ = s.writeFrame(wire.Frame{
			Type:     wire.FrameTypeStreamError,
			StreamID: f.StreamID,
			Payload:  []byte("duplicate stream id"),
		})
		return
	}
	s.streams[f.StreamID] = st
	s.streamsMu.Unlock()
	go h(st)
}

func (s *Session) removeStream(id uint32) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams != nil {
		delete(s.streams, id)
	}
}

// Ping sends a session-level liveness probe. The peer echoes back via Pong.
// The spike doesn't wait for the reply; this is fire-and-forget.
func (s *Session) Ping(payload []byte) error {
	return s.writeFrame(wire.Frame{
		Type:     wire.FrameTypePing,
		StreamID: wire.ControlStreamID,
		Payload:  payload,
	})
}


