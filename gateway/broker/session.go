package broker

import (
	"context"
	"io"
	"sync"

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
		s.ClientCommunicator.Close()
	}

	// Close agent connection
	if s.AgentCommunicator != nil {
		s.AgentCommunicator.Close()
	}

	// Remove from sessions map
	BrokerInstance.sessions.Delete(s.ID)
}

// forward data from agent to tcp
func (s *Session) ForwardToTCP(data []byte) {
	s.mu.Lock()
	if s.closed || s.dataChannel == nil {
		s.mu.Unlock()
		return // Session is closed
	}
	s.mu.Unlock()

	select {
	// the data is create with a buffer size buffer size of 1024
	// Up to 1024 messages can be queued without blocking
	//If the buffer is full, new data is dropped rather than blocking
	case s.dataChannel <- data:
		// Successfully sent
	case <-s.ctx.Done():
		// Session is being closed, don't send
		log.Infof("Session %s closed, dropping data", s.ID)
	}
}

// this will spam data from tcp to agent wsconn
func (s *Session) ForwardToAgent(data []byte) error {
	// Send first RDP packet using simple header format (not WebSocketMessage)
	header := &Header{
		SID: s.ID,
		Len: uint32(len(data)),
	}

	framedData := append(header.Encode(), data...)

	if err := s.SendToAgent(framedData); err != nil {
		log.Infof("Failed to send first RDP packet: %v", err)
		return err
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

			header := &Header{
				SID: s.ID,
				Len: uint32(n),
			}
			framedData := append(header.Encode(), buffer[:n]...)

			if err := s.SendToAgent(framedData); err != nil {
				log.Infof("Failed to send RDP data to agent: %v", err)
				break
			}
		}
	}
	return nil
}

// this will forward data from agent to tcp
func (s *Session) ForwardToClient() {
	for data := range s.dataChannel {

		// Write directly to TCP connection
		if err := s.ClientCommunicator.Send(data); err != nil {
			log.Infof("TCP write error: %v", err)
			break
		}
	}
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
