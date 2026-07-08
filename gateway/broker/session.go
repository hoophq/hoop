package broker

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

type Broker struct {
	agents   sync.Map // map[string]*Connection
	sessions sync.Map // map[uuid.UUID]*Session
}

var BrokerInstance = &Broker{}

// maxQueuedBytes caps how many bytes the agent->client relay queue of a
// single session may hold. When the client drains slower than the agent
// produces (WAN browser vs. LAN agent), the producer blocks in ForwardToTCP
// instead of queueing without bound — backpressure then propagates over the
// agent websocket's TCP flow control into the protocol's own pacing. Before
// this cap existed the queue was bounded only in message count (8192 slots),
// which under RDP-sized frames meant multi-GB of heap and an OOM-killed
// gateway (July 2026 tryrunops incident).
//
// Capacity planning note: this bounds the *queued* bytes. Total in-flight
// data per session is up to maxQueuedBytes plus up to two messages held
// outside the queue (one being written to the client, one unread remainder
// in the conn wrapper) plus whatever the GC has not collected yet — budget
// roughly 2x this constant per hot session.
const maxQueuedBytes = 32 << 20 // 32 MiB

type Session struct {
	ID                 uuid.UUID
	ClientCommunicator ConnectionCommunicator
	AgentCommunicator  ConnectionCommunicator
	Protocol           string

	Connection          models.Connection
	CredentialID        string
	clientAddr          string
	dataChannel         chan []byte
	credentialsReceived chan bool
	closed              bool
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex

	// agent->client relay queue byte accounting (guarded by mu). spaceFreed
	// is a capacity-1 wakeup signal: receivers nudge it after crediting
	// budget back so a producer blocked in ForwardToTCP re-checks.
	queuedBytes   int64
	maxQueueBytes int64
	spaceFreed    chan struct{}
}

func (s *Session) AcknowledgeCredentials() {
	select {
	case s.credentialsReceived <- true:
	default:
	}
}

func (s *Session) SendToAgent(data []byte) error {
	err := s.AgentCommunicator.Send(data)
	if err != nil {
		log.Errorf("Error sending to agent: %v", err)
		return err
	}
	return nil
}

func (s *Session) ReadFromAgent() (int, []byte, error) {
	l, message, err := s.AgentCommunicator.Read()
	return l, message, err
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return // Already closed
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}

	// The data channel is deliberately NOT closed: producers may be blocked
	// in a send racing this close, and closing would turn that race into a
	// send-on-closed-channel panic. Receivers observe termination through
	// ctx.Done() instead; the channel itself is garbage collected with the
	// session.

	// Close consumer connection
	if s.ClientCommunicator != nil {
		_ = s.ClientCommunicator.Close()
	}

	// Close agent connection
	if s.AgentCommunicator != nil {
		_ = s.AgentCommunicator.Close()
	}

	// Remove from the sessions map
	BrokerInstance.sessions.Delete(s.ID)
}

// ForwardToTCP relays data from the agent toward the client. The queue is
// byte-budgeted: when the client is slower than the agent, this call blocks
// once maxQueueBytes are in flight, propagating backpressure to the agent
// websocket instead of growing the heap. A message larger than the whole
// budget is still admitted once the queue is empty, so oversized frames
// cannot deadlock the relay. Data is never silently dropped mid-stream — for
// a byte-oriented protocol like RDP-in-TLS that would corrupt the stream;
// the only discard case is session termination.
func (s *Session) ForwardToTCP(data []byte) {
	if len(data) == 0 {
		return
	}

	// Acquire byte budget.
	for {
		s.mu.Lock()
		if s.closed || s.dataChannel == nil {
			s.mu.Unlock()
			return // Session is closed
		}
		if s.queuedBytes == 0 || s.queuedBytes+int64(len(data)) <= s.maxQueueBytes {
			s.queuedBytes += int64(len(data))
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()

		select {
		case <-s.spaceFreed:
			// budget may have been credited back; re-check
		case <-s.ctx.Done():
			return // Session is being closed
		}
	}

	select {
	case s.dataChannel <- data:
		// Successfully queued
	case <-s.ctx.Done():
		// Session closed while waiting for a channel slot; return the
		// budget so a concurrent producer blocked above can observe it.
		s.creditQueueBytes(int64(len(data)))
	}
}

// creditQueueBytes returns budget to the queue and wakes one producer that
// may be waiting for space.
func (s *Session) creditQueueBytes(n int64) {
	s.mu.Lock()
	s.queuedBytes -= n
	if s.queuedBytes < 0 {
		// Accounting invariant broken — clamp so admission control keeps
		// working, but log loudly: a silent underflow would quietly disable
		// the byte budget.
		log.Errorf("session %s relay queue accounting underflow (%d), clamping to 0", s.ID, s.queuedBytes)
		s.queuedBytes = 0
	}
	s.mu.Unlock()
	select {
	case s.spaceFreed <- struct{}{}:
	default:
	}
}

// receiveData pops the next relay message, crediting its bytes back to the
// producer budget. It returns io.EOF once the session is closed and ctx.Err()
// when the caller-supplied context (read deadline) expires first.
func (s *Session) receiveData(ctx context.Context) ([]byte, error) {
	select {
	case data := <-s.dataChannel:
		s.creditQueueBytes(int64(len(data)))
		return data, nil
	case <-s.ctx.Done():
		// Serve anything already queued before reporting EOF: the closing
		// side may have raced messages into the channel.
		select {
		case data := <-s.dataChannel:
			s.creditQueueBytes(int64(len(data)))
			return data, nil
		default:
			return nil, io.EOF
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) SendRawDataToAgent(data []byte) error {
	header := &Header{
		SID: s.ID,
		Len: uint32(len(data)),
	}

	framedData := append(header.Encode(), data...)

	return s.SendToAgent(framedData)
}

// ForwardToAgent this will spam data from tcp to agent wsconn
func (s *Session) ForwardToAgent(data []byte) error {
	if data != nil {
		// Send first RDP packet using simple header format (not WebSocketMessage)
		if err := s.SendRawDataToAgent(data); err != nil {
			log.Infof("Failed to send first RDP packet: %v", err)
			return err
		}
	}

	// sending first packet done
	// Continue reading from TCP connection (not from agent!)
	for {
		n, buffer, err := s.ClientCommunicator.Read()
		if err != nil {
			if err != io.EOF {
				log.Infof("TCP read error: %v", err)
			}
			break
		}

		if n > 0 {
			if err = s.SendRawDataToAgent(buffer[:n]); err != nil {
				log.Infof("Failed to send RDP data to agent: %v", err)
				break
			}
		}
	}
	return nil
}

// ForwardToClient this will forward data from agent to tcp
func (s *Session) ForwardToClient() {
	for {
		data, err := s.receiveData(context.Background())
		if err != nil {
			return // session closed
		}

		// Write directly to TCP connection
		if err := s.ClientCommunicator.Send(data); err != nil {
			log.Infof("TCP write error: %v", err)
			return
		}
	}
}

// ToConn returns a net.Conn that can be used to read and write as a normal go connection
// Warn: do not use this when calling ForwardToClient()
func (s *Session) ToConn() net.Conn {
	return &sessionConnWrapper{session: s}
}

// AgentCapabilityWait bounds how long a caller will wait for an agent's
// capability advertisement to arrive before treating it as unknown. The
// capability frame is the first thing an agent sends after connecting, so this
// only ever elapses for an old agent that never advertises, or a degenerate
// connection — in which case the caller fails closed. Kept small so a healthy
// new agent's frame (already in flight) is observed without adding meaningful
// latency.
const AgentCapabilityWait = 3 * time.Second

// agentEntry is the broker's per-agent runtime state: the live communicator
// plus any connection-scoped capabilities the agent advertised after
// connecting.
//
//   - `capabilitiesKnown` distinguishes "agent said it cannot" from "agent has
//     not told us yet" — a distinction the PII guard relies on to fail closed
//     on the unknown case rather than silently running a session unguarded.
//   - `ready` is closed exactly once, when the capability frame arrives, so a
//     caller can wait (bounded) for the connect-time advertisement instead of
//     racing it.
//   - `id` identifies this specific connection instance so cleanup on
//     disconnect only removes the entry if it has not already been replaced by
//     a newer connection for the same agent name.
type agentEntry struct {
	id           uuid.UUID
	comm         ConnectionCommunicator
	mu           sync.Mutex
	capabilities map[string]string

	readyMu sync.Mutex // guards readyClosed; ready chan itself is immutable
	ready   chan struct{}
	// readyClosed mirrors "ready is closed" so close happens at most once.
	readyClosed bool
}

func (e *agentEntry) markReady() {
	e.readyMu.Lock()
	defer e.readyMu.Unlock()
	if !e.readyClosed {
		e.readyClosed = true
		close(e.ready)
	}
}

// CreateAgent registers a freshly connected agent and returns an opaque handle
// (its connection-instance id) that the caller must pass to RemoveAgent on
// disconnect. Tying removal to this id prevents a late-closing old connection
// from deleting the entry of a newer connection that reused the same agent
// name.
func CreateAgent(agentID string, ws *websocket.Conn) (uuid.UUID, error) {
	instanceID := uuid.New()
	BrokerInstance.agents.Store(agentID, &agentEntry{
		id:           instanceID,
		comm:         NewAgentCommunicator(ws),
		capabilities: map[string]string{},
		ready:        make(chan struct{}),
	})
	return instanceID, nil
}

// RemoveAgent deletes the agent's broker state on disconnect, but only if the
// currently stored entry is still the one created with instanceID. If a newer
// connection for the same name has already replaced it, this is a no-op — the
// stale connection must not evict the live one.
func RemoveAgent(agentID string, instanceID uuid.UUID) {
	if e, ok := getAgentEntry(agentID); ok && e.id == instanceID {
		BrokerInstance.agents.Delete(agentID)
	}
}

func getAgentEntry(agentID string) (*agentEntry, bool) {
	if v, ok := BrokerInstance.agents.Load(agentID); ok {
		if e, ok := v.(*agentEntry); ok {
			return e, true
		}
	}
	return nil, false
}

func GetAgent(agentID string) (ConnectionCommunicator, bool) {
	if e, ok := getAgentEntry(agentID); ok {
		return e.comm, true
	}
	return nil, false
}

// SetAgentCapabilities records the connection-scoped capabilities advertised
// by an agent and unblocks anyone waiting on the advertisement. Called when a
// Capabilities control frame is received. No-op if the agent is not currently
// registered. The map is defensively copied so later mutation by the caller
// cannot race readers of the stored state.
func SetAgentCapabilities(agentID string, capabilities map[string]string) {
	e, ok := getAgentEntry(agentID)
	if !ok {
		return
	}
	cp := make(map[string]string, len(capabilities))
	for k, v := range capabilities {
		cp[k] = v
	}
	e.mu.Lock()
	e.capabilities = cp
	e.mu.Unlock()
	e.markReady()
}

// AgentCapability reports the value of a single advertised capability and
// whether the agent's capabilities are known at all. If the agent is connected
// but has not advertised yet, it waits up to AgentCapabilityWait for the
// connect-time frame (closing the connect race) before reporting unknown.
//
// The two booleans are distinct on purpose:
//   - known=false: the agent has not advertised capabilities within the wait
//     (old agent, or a degenerate connection). Callers that need a security
//     guarantee should generally treat this as "cannot" and fail closed —
//     EXCEPT where availability across mixed-version rollouts outweighs it: the
//     RDP handler deliberately runs unknown-capability sessions unguarded
//     rather than 403-ing every connection through a not-yet-upgraded agent
//     (see gateway/rdp/irongw.go). Choose per call site, consciously.
//   - known=true, value=false: the agent explicitly cannot do this.
//   - known=true, value=true: the agent can.
func AgentCapability(agentID, key string) (value bool, known bool) {
	e, ok := getAgentEntry(agentID)
	if !ok {
		return false, false
	}

	// Wait (bounded) for the connect-time advertisement if it has not arrived.
	select {
	case <-e.ready:
	case <-time.After(AgentCapabilityWait):
		return false, false
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.capabilities[key] == "true", true
}

func GetSession(sessionId uuid.UUID) *Session {
	if sess, ok := BrokerInstance.sessions.Load(sessionId); ok {
		if session, valid := sess.(*Session); valid {
			return session
		}
	}
	return nil
}

func GetSessions() map[uuid.UUID]*Session {
	sessions := map[uuid.UUID]*Session{}
	BrokerInstance.sessions.Range(func(key, value any) bool {
		if sessionID, ok := key.(uuid.UUID); ok {
			if session, valid := value.(*Session); valid {
				sessions[sessionID] = session
			}
		}
		return true
	})
	return sessions
}

// RevokeByCredentialID closes all sessions using the given credential ID.
// This triggers the same cleanup flow as when a credential expires.
func RevokeByCredentialID(credentialID string) {
	for _, session := range GetSessions() {
		if session != nil && session.CredentialID == credentialID {
			session.Close()
		}
	}
}

var _ net.Conn = (*sessionConnWrapper)(nil)

// sessionConnWrapper makes Session look like a normal net.Conn. The
// unconsumed remainder of the last relay message is retained by reference in
// `pending` and served on subsequent reads — never truncated. (A previous
// version spilled the remainder into a fixed 16 KiB array, silently dropping
// whatever exceeded it and corrupting the byte stream for large messages.)
//
// Field access is mutex-guarded so deadline updates racing a Read (e.g. from
// a tls.Conn that assumes net.Conn thread-safety) stay memory-safe. Byte
// ordering is only meaningful with a single reader goroutine, which is the
// contract of a byte stream anyway.
type sessionConnWrapper struct {
	session  *Session
	mu       sync.Mutex
	deadline *time.Time
	pending  []byte // unread tail of the last message popped from the queue
}

func (s *sessionConnWrapper) Read(b []byte) (n int, err error) {
	// First, serve any buffered data and snapshot the deadline. The lock is
	// NOT held while blocking on the queue below.
	s.mu.Lock()
	if len(s.pending) > 0 {
		n := copy(b, s.pending)
		s.pending = s.pending[n:]
		s.mu.Unlock()
		return n, nil
	}
	deadline := s.deadline
	s.deadline = nil
	s.mu.Unlock()

	ctx := context.Background()
	cancel := func() {}
	if deadline != nil {
		ctx, cancel = context.WithDeadline(ctx, *deadline)
	}
	defer cancel()

	data, err := s.session.receiveData(ctx)
	if err != nil {
		return 0, err
	}

	n = copy(b, data)
	if n < len(data) {
		s.mu.Lock()
		s.pending = data[n:]
		s.mu.Unlock()
	}
	return n, nil
}

func (s *sessionConnWrapper) Write(b []byte) (n int, err error) {
	err = s.session.SendRawDataToAgent(b)
	return len(b), err
}

func (s *sessionConnWrapper) Close() error {
	s.session.Close()
	return nil
}

func (s *sessionConnWrapper) LocalAddr() net.Addr {
	return nil
}

func (s *sessionConnWrapper) RemoteAddr() net.Addr {
	return nil
}

func (s *sessionConnWrapper) SetDeadline(t time.Time) error {
	s.mu.Lock()
	s.deadline = &t
	s.mu.Unlock()
	return nil
}

func (s *sessionConnWrapper) SetReadDeadline(t time.Time) error {
	return s.SetDeadline(t)
}

func (s *sessionConnWrapper) SetWriteDeadline(t time.Time) error {
	return s.SetDeadline(t)
}
