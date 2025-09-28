package broker

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

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

// CreateRDPSession creates a session with protocol-specific
func CreateRDPSession(
	connTcp ConnectionCommunicator,
	connectionInfo models.Connection,
	clientAddr string,
	protocol string,
	extractedCreds string) (*Session, error) {

	sessionID := uuid.New()

	client, _ := GetAgent(connectionInfo.AgentName)
	if client == nil {
		return nil, fmt.Errorf("agent not found: %s", connectionInfo.AgentName)
	}

	dataChannel := make(chan []byte, 1024)
	credentialsReceived := make(chan bool, 1)

	session := &Session{
		ID:                  sessionID,
		ClientCommunicator:  connTcp,
		AgentCommunicator:   client,
		clientAddr:          clientAddr,
		dataChannel:         dataChannel,
		Protocol:            ProtocolRDP,
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
		BrokerInstance.sessions.Delete(sessionID)
		return nil, err
	}

	if err := session.SendToAgent(framedData); err != nil {
		BrokerInstance.sessions.Delete(sessionID)
		return nil, err
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()

	// Wait for protocol-specific started response
	select {
	case <-credentialsReceived:
		return session, nil
	case <-timeoutCtx.Done():
		// Clean up session on timeout
		BrokerInstance.sessions.Delete(sessionID)
		return nil, fmt.Errorf("timeout waiting for %s started response", protocol)
	}
}
