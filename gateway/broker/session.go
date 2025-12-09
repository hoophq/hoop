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

type Session struct {
	ID                 uuid.UUID
	ClientCommunicator ConnectionCommunicator
	AgentCommunicator  ConnectionCommunicator
	Protocol           string

	Connection          models.Connection
	clientAddr          string
	dataChannel         chan []byte
	credentialsReceived chan bool
	closed              bool
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex
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

	// Close data channel safely
	if s.dataChannel != nil {
		close(s.dataChannel)
	}

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

// ForwardToTCP forward data from agent to tcp
func (s *Session) ForwardToTCP(data []byte) {
	s.mu.Lock()
	if s.closed || s.dataChannel == nil {
		s.mu.Unlock()
		return // Session is closed
	}
	s.mu.Unlock()

	select {
	// the data is created with a buffer size of 1024
	// Up to 1024 messages can be queued without blocking
	//If the buffer is full, new data is dropped rather than blocking
	case s.dataChannel <- data:
		// Successfully sent
	case <-s.ctx.Done():
		// Session is being closed, don't send
		log.Infof("Session %s closed, dropping data", s.ID)
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
	for data := range s.dataChannel {

		// Write directly to TCP connection
		if err := s.ClientCommunicator.Send(data); err != nil {
			log.Infof("TCP write error: %v", err)
			break
		}
	}
}

// GetTCPDataChannel returns the channel that will be used to send data to the TCP connection
// Warn: do not use this when calling ForwardToClient()
func (s *Session) GetTCPDataChannel() chan []byte {
	return s.dataChannel
}

// ToConn returns a net.Conn that can be used to read and write as a normal go connection
// Warn: do not use this when calling ForwardToClient()
func (s *Session) ToConn() net.Conn {
	return &sessionConnWrapper{session: s}
}

func CreateAgent(agentID string, ws *websocket.Conn) error {
	BrokerInstance.agents.Store(agentID, NewAgentCommunicator(ws))
	return nil
}

func GetAgent(agentID string) (ConnectionCommunicator, bool) {
	if v, ok := BrokerInstance.agents.Load(agentID); ok {
		if c, ok := v.(ConnectionCommunicator); ok {
			return c, true
		}
	}
	return nil, false
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

var _ net.Conn = (*sessionConnWrapper)(nil)

// sessionConnWrapper makes Session look like a normal net.Conn
type sessionConnWrapper struct {
	session   *Session
	deadline  *time.Time
	buffer    [16384]byte
	bufferPos int // current position in buffer
	bufferLen int // amount of valid data in a buffer
}

func (s *sessionConnWrapper) Read(b []byte) (n int, err error) {
	ctx := context.Background()
	cancel := func() {}
	if s.deadline != nil {
		ctx, cancel = context.WithDeadline(ctx, *s.deadline)
	}
	defer cancel()
	defer func() {
		s.deadline = nil
	}()

	c := s.session.GetTCPDataChannel()

	// First, serve any buffered data
	if s.bufferLen > 0 {
		n := copy(b, s.buffer[s.bufferPos:s.bufferPos+s.bufferLen])
		s.bufferPos += n
		s.bufferLen -= n
		if s.bufferLen == 0 {
			s.bufferPos = 0
		}
		return n, nil
	}

	// Wait for data from a channel or context done
	select {
	case data := <-c:
		if data == nil {
			// Channel closed
			return 0, io.EOF
		}

		// Copy as much as we can into the provided buffer
		remaining := len(b)
		if len(data) > remaining {
			// Buffer the excess data in the internal buffer
			n := copy(b, data[:remaining])

			// Store the rest in the internal buffer
			s.bufferLen = copy(s.buffer[:], data[remaining:])
			s.bufferPos = 0
			return n, nil
		}

		// Data fits entirely in the provided buffer
		n := copy(b, data)
		return n, nil

	case <-ctx.Done():
		return 0, ctx.Err()
	}
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
	s.deadline = &t
	return nil
}

func (s *sessionConnWrapper) SetReadDeadline(t time.Time) error {
	return s.SetDeadline(t)
}

func (s *sessionConnWrapper) SetWriteDeadline(t time.Time) error {
	return s.SetDeadline(t)
}
