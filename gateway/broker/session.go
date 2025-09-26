package broker

import (
//	"context"
//	"encoding/base64"
	"fmt"
	"io"
	"net"
	"sync"
//	"time"

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

type Connection struct {
	ConnType   string `validate:"required,oneof=websocket tcp"`
	Connection any
}

type Session struct {
	ID uuid.UUID
	//change this consumer for something else
	Consumer            *Connection
	Agent               *Connection
	Connection          models.Connection
	clientAddr          string
	dataChannel         chan []byte
	credentialsReceived chan bool
	closed              bool
	mu                  sync.Mutex
}

func (s *Session) AcknowledgeCredentials() {
	select {
	case s.credentialsReceived <- true:
	default:
	}
}

func (s *Session) SendToAgent(data []byte) error {
	switch s.Agent.ConnType {
	case "websocket":
		if wsConn, ok := s.Agent.Connection.(*websocket.Conn); ok {
			return wsConn.WriteMessage(websocket.BinaryMessage, data)
		}
	case "tcp":
		if tcpConn, ok := s.Agent.Connection.(net.Conn); ok {
			_, err := tcpConn.Write(data)
			return err
		}
	default:
		return fmt.Errorf("unsupported agent connection type: %s", s.Agent.ConnType)
	}
	return fmt.Errorf("invalid agent connection")
}

func (s *Session) ReadFromAgent() (int, []byte, error) {
	switch s.Agent.ConnType {
	case "websocket":
		if wsConn, ok := s.Agent.Connection.(*websocket.Conn); ok {
			_, message, err := wsConn.ReadMessage()
			return len(message), message, err
		}
	case "tcp":
		if tcpConn, ok := s.Agent.Connection.(net.Conn); ok {
			buffer := make([]byte, 16*1024)
			n, err := tcpConn.Read(buffer)
			if err != nil {
				return 0, nil, err
			}
			return n, buffer, nil
		}
	default:
		return 0, nil, fmt.Errorf("unsupported agent connection type: %s", s.Agent.ConnType)
	}
	return 0, nil, fmt.Errorf("invalid agent connection")
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.closed {
		return // Already closed
	}
	s.closed = true
	
	// Close data channel safely
	if s.dataChannel != nil {
		close(s.dataChannel)
	}
	
	// Close consumer connection
	if s.Consumer != nil {
		if conn, ok := s.Consumer.Connection.(net.Conn); ok {
			conn.Close()
		}
	}
	
	// Close agent connection
	if s.Agent != nil {
		switch s.Agent.ConnType {
		case "websocket":
			if wsConn, ok := s.Agent.Connection.(*websocket.Conn); ok {
				wsConn.Close()
			}
		case "tcp":
			if tcpConn, ok := s.Agent.Connection.(net.Conn); ok {
				tcpConn.Close()
			}
		}
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
	
	log.Infof("Forwarded %d bytes to TCP session %s", len(data), s.ID)
	select {
	case s.dataChannel <- data:
		// Successfully sent
	default:
		// Channel is full or closed, ignore
		log.Infof("Failed to forward data to TCP session %s (channel full or closed)", s.ID)
	}
}

// this will spam data from tcp to agent wsconn
func (s *Session) StartingForwardind(data []byte) error {
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

	log.Infof("Sent first RDP packet: %d bytes", len(data))

	// Continue reading from TCP connection (not from agent!)
	if tcpConn, ok := s.Consumer.Connection.(net.Conn); ok {
		buffer := make([]byte, 16*1024)
		for {
			n, err := tcpConn.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Infof("TCP read error: %v", err)
				}
				break
			}

			if n > 0 {
				log.Infof("TCP -> Agent: %d bytes for session %s", n, s.ID)

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
	}
	return nil
}

// this will forward data from agent to tcp
func (s *Session) SendAgentToTCP() {
	for data := range s.dataChannel {
		log.Infof("Agent -> TCP: %d bytes for session %s", len(data), s.ID)

		conn, _ := s.Consumer.Connection.(net.Conn)
		if _, err := conn.Write(data); err != nil {
			log.Infof("TCP write error: %v", err)
			break
		}
	}

}


func CreateAgent(agentId string, conn any) error {
	client := &Connection{
		ConnType:   "websocket",
		Connection: conn,
	}

	BrokerInstance.agents.Store(agentId, client)
	return nil
}

func GetAgent(agentId string) (*Connection, bool) {
	if conn, ok := BrokerInstance.agents.Load(agentId); ok {
		if client, valid := conn.(*Connection); valid {
			return client, true
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
