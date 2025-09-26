package broker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	close(s.dataChannel)
	if s.Consumer != nil {
		if conn, ok := s.Consumer.Connection.(net.Conn); ok {
			conn.Close()
		}
	}
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
	BrokerInstance.sessions.Delete(s.ID)
}

// forward data from agent to tcp
func (s *Session) ForwardToTCP(data []byte) {
	log.Infof("Forwarded %d bytes to TCP session %s", len(data), s.ID)
	s.dataChannel <- data
}

// this will spam data from tcp to agent wsconn
func (s *Session) StartingForwardind(data []byte) error {

	header := &Header{
		SID: s.ID,
		Len: uint32(len(data)),
	}

	framedData := append(header.Encode(), data...)

	if err := s.SendToAgent(framedData); err != nil {
		log.Infof("Failed to send first RDP packet: %v", err)
		return nil
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

func CreateRDPSession(
	connTcp *Connection,
	connectionInfo models.Connection,
	proxyuser string,
	clientAddr string) (*Session, error) {

	sessionID := uuid.New()

	client, ok := GetAgent(connectionInfo.AgentName)

	if !ok {
		return nil, fmt.Errorf("agent %s not found", connectionInfo.AgentName)
	}

	dataChannel := make(chan []byte, 1024)
	credentialsReceived := make(chan bool, 1)

	session := &Session{
		ID:                  sessionID,
		Consumer:            connTcp,
		Agent:               client,
		clientAddr:          clientAddr,
		dataChannel:         dataChannel,
		credentialsReceived: credentialsReceived,
	}

	// Store session immediately so it can be found by WebSocket handler
	BrokerInstance.sessions.Store(sessionID, session)

	// Decode base64 env variables
	secrets := map[string]string{}
	for k, v := range connectionInfo.Envs {
		value, _ := base64.StdEncoding.DecodeString(v)
		secrets[k] = string(value)

	}
	// Send session info to agent
	handshakeInfo := map[string]interface{}{
		"session_id":     sessionID.String(),
		"client_address": clientAddr,
		"username":       secrets["envvar:USER"],
		"password":       secrets["envvar:PASS"],
		"target_address": secrets["envvar:HOST"],
		"proxy_user":     proxyuser,
		"message_type":   "session_started",
		"protocol":       "rdp",
	}

	handshakeData, err := json.Marshal(handshakeInfo)

	if err != nil {
		return nil, fmt.Errorf("failed to marshal handshake info: %v", err)
	}

	header := &Header{
		SID: sessionID,
		Len: uint32(len(handshakeData)),
	}

	framedData := append(header.Encode(), handshakeData...)

	if err := session.SendToAgent(framedData); err != nil {
		log.Infof("Failed to send handshake to agent: %v", err)
		return nil, err
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()

	select {
	case <-credentialsReceived:
		return session, nil
	case <-timeoutCtx.Done():
		BrokerInstance.sessions.Delete(sessionID)
		return nil, fmt.Errorf("timeout waiting for RDP started response")
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
