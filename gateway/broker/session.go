package broker

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

// agent struct

type Broker struct {
	agents   sync.Map // map[string]*Connection
	sessions sync.Map // map[uuid.UUID]*Session
}

var BrokerInstance = &Broker{}

//type Connection interface {
//	Send(data []byte) error
//	Receive() ([]byte, error)
//}

type Connection struct {
	ID string `validate:"required,uuid4"`
	//Kind       string `validate:"required,oneof=agent consumer"`
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
		return nil
	}
	return nil
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
		return 0, nil, nil
	}
	return 0, nil, nil
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
	log.Printf("Session %s closed", s.ID)
}

// forward data from agent to tcp
func (s *Session) ForwardToTCP(data []byte) {
	log.Printf("Forwarded %d bytes to TCP session %s", len(data), s.ID)
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
		log.Printf("Failed to send first RDP packet: %v", err)
		return nil
	}

	log.Printf("Sent first RDP packet: %d bytes", len(data))

	// Continue reading from TCP connection (not from agent!)
	if tcpConn, ok := s.Consumer.Connection.(net.Conn); ok {
		buffer := make([]byte, 16*1024)
		for {
			n, err := tcpConn.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("TCP read error: %v", err)
				}
				break
			}

			if n > 0 {
				log.Printf("TCP -> Agent: %d bytes for session %s", n, s.ID)

				header := &Header{
					SID: s.ID,
					Len: uint32(n),
				}
				framedData := append(header.Encode(), buffer[:n]...)

				if err := s.SendToAgent(framedData); err != nil {
					log.Printf("Failed to send RDP data to agent: %v", err)
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
		log.Printf("Agent -> TCP: %d bytes for session %s", len(data), s.ID)

		conn, _ := s.Consumer.Connection.(net.Conn)
		if _, err := conn.Write(data); err != nil {
			log.Printf("TCP write error: %v", err)
			break
		}
	}

}

func MockConnection() models.Connection {
	return models.Connection{
		OrgID:   "org-12345",
		ID:      "conn-67890",
		AgentID: sql.NullString{String: "1", Valid: true},
		Name:    "Mock RDP connection",
		Command: pq.StringArray{},
		Type:    "rdp",
		SubType: sql.NullString{String: "rdpproxy", Valid: true},
		Status:  "active",

		ManagedBy: sql.NullString{String: "admin@company.com", Valid: true},
		Tags:      pq.StringArray{"production", "critical", "replicated"},

		AccessModeRunbooks: "enabled",
		AccessModeExec:     "restricted",
		AccessModeConnect:  "enabled",
		AccessSchema:       "public",

		JiraIssueTemplateID: sql.NullString{String: "JIRA-123", Valid: true},

		// Read-only fields
		RedactEnabled: false,
		Reviewers:     pq.StringArray{"alice@company.com", "bob@company.com"},
		RedactTypes:   pq.StringArray{"password", "token"},
		AgentMode:     "inline",
		AgentName:     "Main Agent",
		JiraTransitionNameOnClose: sql.NullString{
			String: "Done",
			Valid:  true,
		},
		Envs: map[string]string{
			"Username":  "chico",
			"Address":   "10.211.55.6:3389",
			"Password":  "090994",
			"ProxyUser": "fake",
		},
		GuardRailRules: pq.StringArray{},
		ConnectionTags: map[string]string{
			"team": "platform",
			"tier": "gold",
		},
	}
}

func CreateSession(
	connTcp *Connection,
	connectionInfo models.Connection,
	clientAddr string) (*Session, error) {

	sessionID := uuid.New()
	fmt.Println("Creating session with ID:", sessionID)
	fmt.Println("Connection Info:", connectionInfo.AgentID.String)
	client, _ := GetAgent(connectionInfo.AgentID.String)
	fmt.Println("Agent found:", client != nil)

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

	// Send session info to agent
	handshakeInfo := map[string]interface{}{
		"session_id":     sessionID.String(),
		"client_address": clientAddr,
		"username":       connectionInfo.Envs["Username"],
		"password":       connectionInfo.Envs["Password"],
		"target_address": connectionInfo.Envs["Address"],
		"proxy_user":     connectionInfo.Envs["ProxyUser"],
		"message_type":   "session_started",
		"protocol":       "rdp",
	}
	handshakeData, err := json.Marshal(handshakeInfo)

	if err != nil {
		log.Printf("Failed to marshal handshake info: %v", err)
		return nil, err
	}

	header := &Header{
		SID: sessionID,
		Len: uint32(len(handshakeData)),
	}

	framedData := append(header.Encode(), handshakeData...)

	if err := session.SendToAgent(framedData); err != nil {
		log.Printf("Failed to send handshake to agent: %v", err)
		return nil, err
	}

	log.Printf("Sent handshake to agent for session %s: %d bytes", sessionID, len(handshakeData))

	// Wait for RDP started response
	select {
	case <-credentialsReceived:
		log.Printf("Received RDP started response for session %s", sessionID)
		return session, nil
	case <-time.After(5 * time.Second):
		log.Printf("Timeout waiting for RDP started response for session %s", sessionID)
		// Clean up session on timeout
		BrokerInstance.sessions.Delete(sessionID)
		return nil, fmt.Errorf("timeout waiting for RDP started response")
	}
	//try to do the handshake here

}

func CreateAgent(agentId string, conn any) error {

	client := &Connection{
		ID:         agentId,
		ConnType:   "websocket",
		Connection: conn,
	}

	BrokerInstance.agents.Store(client.ID, client)
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
