package broker

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

// ProtocolHandler defines the interface for protocol-specific handlers
type ProtocolHandler interface {
	HandleSessionStarted(session *Session, msg *WebSocketMessage) error
	HandleData(session *Session, msg *WebSocketMessage) error
	GetProtocolName() string
}

type RDPHandler struct{}

func (h *RDPHandler) GetProtocolName() string {
	return ProtocolRDP
}

func (h *RDPHandler) HandleSessionStarted(session *Session, msg *WebSocketMessage) error {
	session.AcknowledgeCredentials()
	return nil
}

func (h *RDPHandler) HandleData(session *Session, msg *WebSocketMessage) error {
	session.ForwardToTCP(msg.Payload)
	return nil
}

// ProtocolManager manages protocol handlers
type ProtocolManager struct {
	handlers map[string]ProtocolHandler
}

var ProtocolManagerInstance = &ProtocolManager{
	handlers: make(map[string]ProtocolHandler),
}

func init() {
	// Register default protocol handlers
	// if you have more protocols, register them here
	// e.g., ProtocolManagerInstance.RegisterHandler(&SSHHandler{})
	// and need to implement only the SSHHandler struct with HandleSessionStarted and HandleData methods
	// and in the start session you can do as many handshackes as you want
	ProtocolManagerInstance.RegisterHandler(&RDPHandler{})
}

func (pm *ProtocolManager) RegisterHandler(handler ProtocolHandler) {
	pm.handlers[handler.GetProtocolName()] = handler
}

func (pm *ProtocolManager) GetHandler(protocol string) (ProtocolHandler, bool) {
	handler, exists := pm.handlers[protocol]
	return handler, exists
}

// HandleWebSocketMessage handles incoming WebSocket messages using protocol handlers
func HandleWebSocketMessage(data []byte) {
	// Decode WebSocket message
	sessionID, msg, err := DecodeWebSocketMessage(data)
	if err != nil {
		log.Printf("Failed to decode WebSocket message: %v", err)
		return
	}

	session := GetSession(sessionID)
	if session == nil {
		log.Printf("No session found for SID: %s", sessionID)
		return
	}

	// Get protocol from metadata
	protocol, exists := msg.Metadata["protocol"]
	if !exists {
		log.Printf("No protocol specified in message for session: %s", sessionID)
		return
	}

	// Get protocol handler we can use to handle this message
	// can be RDP, SSH, VNC, etc
	handler, exists := ProtocolManagerInstance.GetHandler(protocol)
	if !exists {
		log.Infof("No handler found for protocol: %s", protocol)
		return
	}

	// Handle message based on type
	switch msg.Type {
	case MessageTypeSessionStarted:
		if err := handler.HandleSessionStarted(session, msg); err != nil {
			log.Printf("Error handling session started for protocol %s: %v", protocol, err)
		}
	case MessageTypeData:
		if err := handler.HandleData(session, msg); err != nil {
			log.Printf("Error handling data for protocol %s: %v", protocol, err)
		}
	default:
		log.Printf("Unknown message type: %s for session: %s", msg.Type, sessionID)
	}
}

// CreateSessionWithProtocol creates a session with protocol-specific handling
func CreateSessionWithProtocol(
	connTcp *Connection,
	connectionInfo models.Connection,
	clientAddr string,
	protocol string,
	extractedCreds string) (*Session, error) {

	sessionID := uuid.New()
	log.Printf("Creating %s session with ID: %s", protocol, sessionID)

	client, _ := GetAgent(connectionInfo.AgentName)
	if client == nil {
		return nil, fmt.Errorf("agent not found: %s", connectionInfo.AgentName)
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

	// Decode base64 env variables for RDP
	secrets := map[string]string{}
	for k, v := range connectionInfo.Envs {
		value, _ := base64.StdEncoding.DecodeString(v)
		secrets[k] = string(value)
	}

	// Send session info to agent using new message format
	msg := &WebSocketMessage{
		Type: MessageTypeSessionStarted,
		Metadata: map[string]string{
			"protocol":       protocol,
			"client_address": clientAddr,
			"username":       secrets["envvar:USER"],
			"password":       secrets["envvar:PASS"],
			"target_address": secrets["envvar:HOST"],
			"proxy_user":     extractedCreds, // Use the extracted credentials as proxy_user
		},
		Payload: []byte{}, // Empty payload since session ID is in header
	}

	framedData, err := EncodeWebSocketMessage(sessionID, msg)
	if err != nil {
		log.Printf("Failed to encode handshake message: %v", err)
		BrokerInstance.sessions.Delete(sessionID)
		return nil, err
	}

	if err := session.SendToAgent(framedData); err != nil {
		log.Printf("Failed to send handshake to agent: %v", err)
		BrokerInstance.sessions.Delete(sessionID)
		return nil, err
	}

	log.Printf("Sent handshake to agent for session %s: %d bytes", sessionID, len(framedData))

	// Wait for protocol-specific started response
	select {
	case <-credentialsReceived:
		log.Printf("Received %s started response for session %s", protocol, sessionID)
		return session, nil
	case <-time.After(5 * time.Second):
		log.Printf("Timeout waiting for %s started response for session %s", protocol, sessionID)
		// Clean up session on timeout
		BrokerInstance.sessions.Delete(sessionID)
		return nil, fmt.Errorf("timeout waiting for %s started response", protocol)
	}
}
