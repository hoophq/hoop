package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
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

// RDPGuardConfig carries the agent-side PII guard decision and tuning into
// the SessionStarted metadata. When Enabled is true, the agent runs the
// realtime hold-and-release PII gate on the server->client stream (and the
// gateway suppresses its own gate — single enforcement point). The endpoints
// (Presidio, OCR sidecar) are NOT sent on the wire: the agent reads those
// from its own environment, keeping customer-network infra out of gateway
// state. Only the enable decision and analysis policy travel here.
type RDPGuardConfig struct {
	Enabled        bool
	ScoreThreshold float64
	EntityDenylist []string
	BandPadding    int
	// Policy is "kill", "redact", or "redact_and_kill" (agent default kill
	// when empty/unrecognized).
	Policy string
}

// CreateRDPSession creates a session with protocol-specific
func CreateRDPSession(
	connTcp ConnectionCommunicator,
	connectionInfo models.Connection,
	clientAddr string,
	protocol string,
	extractedCreds string,
	credentialID string,
	expireAt time.Time,
	ctxDuration time.Duration,
	guard RDPGuardConfig,
) (*Session, error) {

	sessionID := uuid.New()

	ctx, timeoutCancelFn := context.WithTimeoutCause(context.Background(), ctxDuration,
		fmt.Errorf("connection access expired (%v)",
			expireAt.Format(time.RFC3339)))

	client, _ := GetAgent(connectionInfo.AgentName)
	if client == nil {
		timeoutCancelFn()
		return nil, fmt.Errorf("agent not found: %s", connectionInfo.AgentName)
	}

	// Slot count only decouples producer and consumer scheduling; the real
	// bound on queued data is the byte budget (maxQueuedBytes) enforced by
	// ForwardToTCP.
	dataChannel := make(chan []byte, 1024)
	credentialsReceived := make(chan bool, 1)

	session := &Session{
		ID:                  sessionID,
		ClientCommunicator:  connTcp,
		AgentCommunicator:   client,
		Connection:          connectionInfo,
		CredentialID:        credentialID,
		clientAddr:          clientAddr,
		dataChannel:         dataChannel,
		Protocol:            ProtocolRDP,
		credentialsReceived: credentialsReceived,
		ctx:                 ctx,
		cancel:              timeoutCancelFn,
		maxQueueBytes:       maxQueuedBytes,
		spaceFreed:          make(chan struct{}, 1),
	}

	// Store session immediately so it can be found by WebSocket handler
	BrokerInstance.sessions.Store(sessionID, session)

	// Decode base64 env variables for RDP
	secrets := map[string]string{}
	for k, v := range connectionInfo.Envs {
		value, _ := base64.StdEncoding.DecodeString(v)
		secrets[k] = string(value)
	}

	host := secrets["envvar:HOST"]
	port := secrets["envvar:PORT"]
	address := fmt.Sprintf("%s:%s", host, port)

	// Send session info to agent using new message format
	metadata := map[string]string{
		"protocol":       protocol,
		"client_address": clientAddr,
		"username":       secrets["envvar:USER"],
		"password":       secrets["envvar:PASS"],
		"target_address": address,
		"proxy_user":     extractedCreds, // Use the extracted credentials as proxy_user
	}
	// Agent-side PII guard: only signal "guard this session" plus analysis
	// policy. The agent supplies Presidio/OCR endpoints from its own env.
	// Absent keys mean "no guard" — an older agent simply ignores them.
	if guard.Enabled {
		metadata["pii_guard"] = "enabled"
		metadata["pii_score_threshold"] = strconv.FormatFloat(guard.ScoreThreshold, 'f', -1, 64)
		metadata["pii_band_padding"] = strconv.Itoa(guard.BandPadding)
		if guard.Policy != "" {
			metadata["pii_policy"] = guard.Policy
		}
		if len(guard.EntityDenylist) > 0 {
			// JSON array, not a comma-join: entity names are an external
			// (Presidio) vocabulary and must not rely on being comma-free.
			if denylist, err := json.Marshal(guard.EntityDenylist); err == nil {
				metadata["pii_entity_denylist"] = string(denylist)
			}
		}
	}
	msg := &WebSocketMessage{
		Type:     MessageTypeSessionStarted,
		Metadata: metadata,
		Payload:  []byte{}, // Empty payload since session ID is in header
	}

	// abort releases the session's context (unblocking any relay producer
	// already racing data in) and deregisters it. It deliberately does NOT
	// call session.Close(): that would close the AgentCommunicator, which is
	// the agent's shared websocket — killing every other session on that
	// agent over a single failed session setup. The client TCP connection is
	// owned and closed by the caller on error.
	abort := func() {
		timeoutCancelFn()
		BrokerInstance.sessions.Delete(sessionID)
	}

	framedData, err := EncodeWebSocketMessage(sessionID, msg)
	if err != nil {
		abort()
		return nil, err
	}

	if err := session.SendToAgent(framedData); err != nil {
		abort()
		return nil, err
	}

	timeoutCtx, cancelFunc := context.WithTimeout(
		context.Background(), 20*time.Second)

	defer cancelFunc()

	// Wait for protocol-specific started response
	select {
	case <-credentialsReceived:
		return session, nil
	case <-timeoutCtx.Done():
		// Clean up session on timeout
		abort()
		return nil, fmt.Errorf("timeout waiting for %s started response", protocol)
	}
}
