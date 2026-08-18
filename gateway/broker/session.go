package broker

import (
	"context"
	"errors"
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
	agents      sync.Map // map[string]*Connection
	sessions    sync.Map // map[uuid.UUID]*Session
	auditRoutes sync.Map // map[uuid.UUID]*SessionAuditRoute
}

var BrokerInstance = &Broker{}

const sessionAuditRouteRetention = 5 * time.Minute

// SessionAuditRoute is the minimal immutable handoff needed to persist a
// terminal agent report after live relay teardown removed the Session.
type SessionAuditRoute struct {
	DatabaseSessionID string
	OrgID             string
	AgentInstanceID   uuid.UUID

	mu    sync.Mutex
	timer *time.Timer
}

func storeSessionAuditRoute(
	routeID uuid.UUID,
	databaseSessionID string,
	orgID string,
	agentInstanceID uuid.UUID,
) {
	BrokerInstance.auditRoutes.Store(routeID, &SessionAuditRoute{
		DatabaseSessionID: databaseSessionID,
		OrgID:             orgID,
		AgentInstanceID:   agentInstanceID,
	})
}

func GetSessionAuditRoute(routeID uuid.UUID) *SessionAuditRoute {
	value, ok := BrokerInstance.auditRoutes.Load(routeID)
	if !ok {
		return nil
	}
	route, _ := value.(*SessionAuditRoute)
	return route
}

func retainSessionAuditRoute(routeID uuid.UUID) {
	route := GetSessionAuditRoute(routeID)
	if route == nil {
		return
	}
	route.mu.Lock()
	defer route.mu.Unlock()
	if route.timer != nil {
		return
	}
	route.timer = time.AfterFunc(sessionAuditRouteRetention, func() {
		BrokerInstance.auditRoutes.CompareAndDelete(routeID, route)
	})
}

func deleteSessionAuditRoute(routeID uuid.UUID) {
	value, ok := BrokerInstance.auditRoutes.LoadAndDelete(routeID)
	if !ok {
		return
	}
	if route, ok := value.(*SessionAuditRoute); ok {
		route.mu.Lock()
		if route.timer != nil {
			route.timer.Stop()
		}
		route.mu.Unlock()
	}
}

// maxQueuedBytes caps queued agent->client data for one session. Admission is
// nonblocking: a slow client terminates only its own session instead of
// stalling the shared agent WebSocket reader and every other session.
const maxQueuedBytes = 32 << 20 // 32 MiB

var (
	ErrSessionClosed         = errors.New("session closed")
	ErrSessionRelayQueueFull = errors.New("session relay queue full")
	errReadDeadlineChanged   = errors.New("read deadline changed")
)

type Session struct {
	ID uuid.UUID
	// DatabaseSessionID identifies the durable audit/recording row. ID above
	// is an independent broker wire ID used to route frames through the agent.
	DatabaseSessionID string
	// AgentInstanceID binds this SID to the exact WebSocket connection that
	// accepted it. A stale connection teardown must not close replacement
	// sessions belonging to a newer connection with the same agent name.
	AgentInstanceID    uuid.UUID
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

	// agent->client relay queue byte accounting, guarded by mu.
	queuedBytes   int64
	maxQueueBytes int64
}

func (s *Session) AcknowledgeCredentials() {
	select {
	case s.credentialsReceived <- true:
	default:
	}
}

func (s *Session) SendToAgent(data []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	agent := s.AgentCommunicator
	s.mu.Unlock()
	if agent == nil {
		return ErrSessionClosed
	}
	if err := agent.Send(data); err != nil {
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
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	client := s.ClientCommunicator
	s.mu.Unlock()

	// The data channel is deliberately not closed: producers can race Close,
	// and receivers observe termination through ctx.Done(). The shared agent
	// communicator is owned by HandleConnection, not by any one session.
	if client != nil {
		_ = client.Close()
	}
	BrokerInstance.sessions.Delete(s.ID)
	retainSessionAuditRoute(s.ID)
}

// ForwardToTCP admits one agent->client relay message without blocking. The
// existing per-session consumer drains dataChannel. A full byte or slot budget
// is terminal for this session: blocking here would head-of-line block the
// shared agent WebSocket reader, including other sessions and control frames.
func (s *Session) ForwardToTCP(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	n := int64(len(data))
	s.mu.Lock()
	if s.closed || s.dataChannel == nil {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if n > s.maxQueueBytes || s.queuedBytes+n > s.maxQueueBytes {
		s.mu.Unlock()
		return ErrSessionRelayQueueFull
	}
	s.queuedBytes += n
	channel := s.dataChannel
	ctx := s.ctx
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		s.creditQueueBytes(n)
		return ErrSessionClosed
	default:
	}

	select {
	case channel <- data:
		return nil
	default:
		s.creditQueueBytes(n)
		return ErrSessionRelayQueueFull
	}
}

func (s *Session) creditQueueBytes(n int64) {
	s.mu.Lock()
	s.queuedBytes -= n
	if s.queuedBytes < 0 {
		log.Errorf("session %s relay queue accounting underflow (%d), clamping to 0", s.ID, s.queuedBytes)
		s.queuedBytes = 0
	}
	s.mu.Unlock()
}

// receiveData pops the next relay message. The caller credits bytes only as it
// consumes them, so data retained by sessionConnWrapper remains inside the
// strict per-session budget. A deadline update wakes the current call so its
// caller can rebuild the context from the new absolute deadline.
func (s *Session) receiveData(
	ctx context.Context,
	deadlineChanged <-chan struct{},
) ([]byte, error) {
	select {
	case data := <-s.dataChannel:
		return data, nil
	case <-s.ctx.Done():
		// Serve anything already queued before reporting EOF: the closing
		// side may have raced messages into the channel.
		select {
		case data := <-s.dataChannel:
			return data, nil
		default:
			return nil, io.EOF
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-deadlineChanged:
		return nil, errReadDeadlineChanged
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
			if err := s.SendRawDataToAgent(buffer[:n]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ForwardToClient forwards queued agent data to the client connection.
func (s *Session) ForwardToClient() {
	for {
		data, err := s.receiveData(context.Background(), nil)
		if err != nil {
			return // session closed
		}

		// Keep the bytes charged while the client write is in flight. This
		// makes the budget cover channel storage plus the consumer's current
		// message, not only messages still waiting in the channel.
		err = s.ClientCommunicator.Send(data)
		s.creditQueueBytes(int64(len(data)))
		if err != nil {
			log.Infof("TCP write error: %v", err)
			return
		}
	}
}

// ToConn returns a net.Conn that can be used to read and write as a normal go connection
// Warn: do not use this when calling ForwardToClient()
func (s *Session) ToConn() net.Conn {
	return &sessionConnWrapper{
		session:         s,
		deadlineChanged: make(chan struct{}),
	}
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
// disconnect. Registration atomically replaces and closes any older
// same-name connection; closing its socket wakes its HandleConnection owner,
// which drains that instance's report workers and closes only its sessions.
func CreateAgent(agentID string, ws *websocket.Conn) (uuid.UUID, error) {
	return registerAgent(agentID, NewAgentCommunicator(ws)), nil
}

func registerAgent(agentID string, comm ConnectionCommunicator) uuid.UUID {
	instanceID := uuid.New()
	replacement := &agentEntry{
		id:           instanceID,
		comm:         comm,
		capabilities: map[string]string{},
		ready:        make(chan struct{}),
	}
	previous, replaced := BrokerInstance.agents.Swap(agentID, replacement)
	if replaced {
		if entry, ok := previous.(*agentEntry); ok && entry.comm != nil {
			if err := entry.comm.Close(); err != nil {
				log.With("agent", agentID).Warnf("failed closing superseded agent connection: %v", err)
			}
		}
	}
	return instanceID
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

// GetAgent returns the current communicator together with the immutable
// connection-instance ID that owns any session created through it.
func GetAgent(agentID string) (ConnectionCommunicator, uuid.UUID, bool) {
	if e, ok := getAgentEntry(agentID); ok {
		return e.comm, e.id, true
	}
	return nil, uuid.Nil, false
}

// GetAgentInstance returns the communicator only when it still belongs to the
// WebSocket connection currently handling a frame.
func GetAgentInstance(agentID string, instanceID uuid.UUID) (ConnectionCommunicator, bool) {
	if e, ok := getAgentEntry(agentID); ok && e.id == instanceID {
		return e.comm, true
	}
	return nil, false
}

// AgentUsesFrameProtocolV2 reports the negotiated framing mode for one exact
// connection instance without waiting for its capability advertisement.
func AgentUsesFrameProtocolV2(agentID string, instanceID uuid.UUID) bool {
	e, ok := getAgentEntry(agentID)
	if !ok || e.id != instanceID {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.capabilities[CapabilityFrameProtocol] == FrameProtocolV2
}

// SetAgentCapabilities records capabilities only when they came from the
// currently registered WebSocket instance. A stale connection must not mark a
// replacement connection ready or overwrite its capability set.
func SetAgentCapabilities(
	agentID string,
	instanceID uuid.UUID,
	capabilities map[string]string,
) {
	e, ok := getAgentEntry(agentID)
	if !ok || e.id != instanceID {
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
//     (old agent, or a degenerate connection). Security-sensitive callers must
//     treat this as "cannot" and fail closed.
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

// GetSessionForAgentInstance returns a live SID only to the exact agent
// WebSocket connection that accepted it.
func GetSessionForAgentInstance(sessionID, instanceID uuid.UUID) *Session {
	session := GetSession(sessionID)
	if session == nil || session.AgentInstanceID != instanceID {
		return nil
	}
	return session
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

// CloseAgentInstanceSessions releases only sessions routed through one exact
// agent WebSocket connection. Same-name replacement connections are isolated.
func CloseAgentInstanceSessions(instanceID uuid.UUID) {
	for _, session := range GetSessions() {
		if session != nil && session.AgentInstanceID == instanceID {
			session.Close()
		}
	}
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

// sessionConnWrapper makes Session look like a normal net.Conn. The
// unconsumed remainder of the last relay message is retained by reference in
// `pending` and served on subsequent reads, with its bytes charged against the
// per-session budget until consumed.
//
// readMu serializes stream consumption as required to preserve byte order
// across concurrent net.Conn readers. mu remains separate so SetReadDeadline
// can wake a blocked Read without waiting for it to return.
type sessionConnWrapper struct {
	session         *Session
	readMu          sync.Mutex
	mu              sync.Mutex
	readDeadline    time.Time
	writeDeadline   time.Time
	deadlineChanged chan struct{}
	pending         []byte
}

var _ net.Conn = (*sessionConnWrapper)(nil)

func (s *sessionConnWrapper) Read(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}

	s.readMu.Lock()
	defer s.readMu.Unlock()

	for {
		// First, serve any buffered data and snapshot the deadline. The lock is
		// NOT held while blocking on the queue below.
		s.mu.Lock()
		if len(s.pending) > 0 {
			n := copy(b, s.pending)
			s.pending = s.pending[n:]
			s.mu.Unlock()
			s.session.creditQueueBytes(int64(n))
			return n, nil
		}
		deadline := s.readDeadline
		deadlineChanged := s.deadlineChanged
		s.mu.Unlock()

		ctx := context.Background()
		cancel := func() {}
		if !deadline.IsZero() {
			ctx, cancel = context.WithDeadline(ctx, deadline)
		}
		data, err := s.session.receiveData(ctx, deadlineChanged)
		cancel()
		if errors.Is(err, errReadDeadlineChanged) {
			continue
		}
		if err != nil {
			return 0, err
		}

		n = copy(b, data)
		if n < len(data) {
			s.mu.Lock()
			s.pending = data[n:]
			s.mu.Unlock()
		}
		s.session.creditQueueBytes(int64(n))
		return n, nil
	}
}

func (s *sessionConnWrapper) Write(b []byte) (n int, err error) {
	s.mu.Lock()
	deadline := s.writeDeadline
	s.mu.Unlock()
	if !deadline.IsZero() && !time.Now().UTC().Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	if err := s.session.SendRawDataToAgent(b); err != nil {
		return 0, err
	}
	return len(b), nil
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
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

func (s *sessionConnWrapper) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	close(s.deadlineChanged)
	s.deadlineChanged = make(chan struct{})
	s.mu.Unlock()
	return nil
}

func (s *sessionConnWrapper) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.writeDeadline = t
	s.mu.Unlock()
	return nil
}
