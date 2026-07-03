package transport

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
)

var upgrader = websocket.Upgrader{
	//ReadBufferSize:  32 << 10, // 32 KiB (tune as needed)
	//WriteBufferSize: 32 << 10,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: tighten origin checks later
		return true
	},
	EnableCompression: true,
}

func verifyWebsocketToken(token string) (*models.Agent, error) {
	dsn, err := dsnkeys.Parse(token)
	if err != nil {
		log.With("token_length", len(token)).Errorf("invalid agent authentication (dsn), err=%v", err)
		return nil, err
	}

	ag, err := models.GetAgentByToken(dsn.SecretKeyHash)
	if err != nil {
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, agent-token=%v, err=%v", len(token), token, err)
		return nil, err
	}
	if ag.Name != dsn.Name {
		log.Errorf("failed authenticating agent (agent dsn), mismatch dsn attributes. id=%v, name=%v, mode=%v",
			ag.ID, dsn.Name, dsn.AgentMode)
		return nil, fmt.Errorf("agent dsn mismatch")
	}
	return ag, nil
}

func HandleConnection(c *gin.Context) {
	token := c.Request.Header.Get("HOOP_KEY")
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing HOOP_KEY header"})
		return
	}

	agent, err := verifyWebsocketToken(token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authentication"})
		return
	}

	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("WebSocket upgrade error: %v", err)
		return
	}
	defer wsConn.Close()

	// Register this agent's communicator inside the broker. The returned
	// instance id ties cleanup to THIS connection so a late close of a stale
	// connection cannot evict a newer one that reused the same agent name.
	agentInstanceID, err := broker.CreateAgent(agent.Name, wsConn)
	if err != nil {
		log.Errorf("failed to register agent communicator: %v", err)
		return
	}

	log.Debugf("WebSocket connection established for agent=%s", agent.Name)

	for {
		messageType, data, err := wsConn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				log.Debugf("WebSocket closed for agent=%s: %v", agent.Name, err)
			} else {
				log.Infof("WebSocket read error for agent=%s: %v", agent.Name, err)
				break
			}
			break
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		handleWebSocketMessage(agent.Name, data)
	}

	// Cleanup: remove this connection's broker state (only if it is still the
	// live entry for this agent name) so a late close of a stale connection
	// cannot evict a newer one that reused the same name.
	broker.RemoveAgent(agent.Name, agentInstanceID)

	// Then close sessions belonging to this agent. NOTE: this still matches by
	// agent NAME, not connection instance, so if the same name reconnects with
	// overlapping sessions, a stale connection's cleanup can close the newer
	// connection's sessions. Pre-existing behavior; tracked in RD-247 to scope
	// teardown by connection instance. The broker-entry fix above already
	// prevents the worst case (stale eviction of the live registration).
	for id, s := range broker.GetSessions() {
		if s.Connection.AgentName == agent.Name {
			log.Debugf("Closing session %s due to agent=%s disconnect", id, agent.Name)
			s.Close()
		}
	}
}

// agentGuardrailsViolation mirrors the Rust agent's ViolationReport
// (agentrs/src/piigate/report.rs). Entity metadata only — no pixels/text.
type agentGuardrailsViolation struct {
	Kind        string   `json:"kind"` // "detection" | "overload" | "analysis_error"
	EntityTypes []string `json:"entity_types"`
	Detections  []struct {
		EntityType string  `json:"entity_type"`
		Score      float64 `json:"score"`
		X          int     `json:"x"`
		Y          int     `json:"y"`
		Width      int     `json:"width"`
		Height     int     `json:"height"`
	} `json:"detections"`
	DroppedBytes int `json:"dropped_bytes"`
}

// persistAgentGuardrailsViolation records an agent-reported PII guard
// violation as session guardrails info plus per-entity detection rows —
// mirroring the gateway-side gate's persistPIIViolation. Best-effort and
// non-transactional: enforcement already happened at the agent, partial
// evidence beats none.
func persistAgentGuardrailsViolation(s *broker.Session, payload []byte) {
	orgID := s.Connection.OrgID
	sessionID := s.ID.String()

	var report agentGuardrailsViolation
	if err := json.Unmarshal(payload, &report); err != nil {
		log.With("sid", sessionID).Warnf("piigate: failed to decode agent guardrails violation: %v", err)
		return
	}

	// Distinguish the fail-closed causes from a real PII detection in the
	// audit record: an overload (analysis backlog) and an analysis error
	// (OCR/Presidio failure or timeout) both terminate the session without
	// confirmed entities, and must not be mislabeled as a detection.
	ruleType := "pii_detection"
	switch report.Kind {
	case "overload":
		ruleType = "pii_guard_overload"
	case "analysis_error":
		ruleType = "pii_guard_analysis_error"
	}
	info := []models.SessionGuardRailsInfo{{
		RuleName:     "rdp_pii_guard",
		Rule:         models.SessionGuardRailMatchedRule{Type: ruleType},
		Direction:    "server_to_client",
		MatchedWords: report.EntityTypes,
	}}
	if data, err := json.Marshal(info); err != nil {
		log.With("sid", sessionID).Warnf("piigate: failed to marshal agent guardrails info: %v", err)
	} else if err := models.UpdateSessionGuardRailsInfo(orgID, sessionID, data); err != nil {
		log.With("sid", sessionID).Warnf("piigate: failed to persist agent guardrails info: %v", err)
	}

	if len(report.Detections) > 0 {
		detections := make([]models.RDPEntityDetection, 0, len(report.Detections))
		for _, d := range report.Detections {
			detections = append(detections, models.RDPEntityDetection{
				SessionID:  sessionID,
				EntityType: d.EntityType,
				Score:      d.Score,
				X:          d.X,
				Y:          d.Y,
				Width:      d.Width,
				Height:     d.Height,
			})
		}
		if err := models.BulkInsertRDPEntityDetections(detections); err != nil {
			log.With("sid", sessionID).Warnf("piigate: failed to persist agent entity detections: %v", err)
		}
	}

	log.With("sid", sessionID).Infof("piigate: agent-side PII guard violation persisted (kind=%s, entities=%v)", report.Kind, report.EntityTypes)
}

func handleWebSocketMessage(agentName string, data []byte) {
	// 1) Try CONTROL frame first (JSON-like)
	if sid, msg, err := broker.DecodeWebSocketMessage(data); err == nil {
		// Connection-scoped control frames (sid == ControlSentinelSID) describe
		// the agent connection, not a session. Dispatch them by type before any
		// session lookup.
		if sid == broker.ControlSentinelSID {
			switch msg.Type {
			case broker.MessageTypeCapabilities:
				broker.SetAgentCapabilities(agentName, msg.Metadata)
				log.Debugf("agent=%s advertised capabilities: %v", agentName, msg.Metadata)
			default:
				log.Infof("Unhandled connection-scoped control message type=%q for agent=%s", msg.Type, agentName)
			}
			return
		}

		s := broker.GetSession(sid)
		if s == nil {
			log.Infof("Control message for unknown SID=%s", sid)
			return
		}
		handler, ok := broker.ProtocolManagerInstance.GetHandler(s.Protocol)
		if !ok {
			log.Infof("No protocol handler for %q (SID=%s)", s.Protocol, sid)
			return
		}

		switch msg.Type {
		case broker.MessageTypeSessionStarted: // can add more case to handle session start acknowledgement, or initial connection
			// for multiple protocols
			_ = handler.HandleSessionStarted(s, msg) // handler decides when to ack via session.AcknowledgeCredentials()
		case broker.MessageTypeData:
			// Some agents might send data via control envelope. Let handler decide.
			_ = handler.HandleData(s, msg)
		case broker.MessageTypeGuardrailsViolation:
			// Agent-side PII guard reported a violation: persist the entity
			// metadata for audit. Enforcement (session teardown) already
			// happened at the agent — this is best-effort evidence.
			persistAgentGuardrailsViolation(s, msg.Payload)
		default:
			log.Infof("Unhandled control message type=%q for SID=%s", msg.Type, sid)
		}
		return
	}

	// 2) Try FRAMED BINARY (stream data for ANY TCP-like protocol)
	if header, headerLen, err := broker.DecodeHeader(data); err == nil {
		if len(data) < headerLen {
			log.Infof("Framed payload truncated (len=%d < headerLen=%d)", len(data), headerLen)
			return
		}
		payload := data[headerLen:]

		s := broker.GetSession(header.SID)
		if s == nil {
			log.Infof("Binary payload for unknown SID=%s", header.SID)
			return
		}

		handler, ok := broker.ProtocolManagerInstance.GetHandler(s.Protocol)
		if ok {
			// Wrap as a data message for a uniform handler entrypoint.
			dm := &broker.WebSocketMessage{
				Type:     broker.MessageTypeData,
				Metadata: map[string]string{"transport": "binary"},
				Payload:  payload,
			}
			if err := handler.HandleData(s, dm); err == nil {
				return // handler consumed it
			}
			// else fallthrough to generic forwarding or add error
			log.Infof("Protocol handler %q failed to handle binary data: %v", s.Protocol, err)
		}

		// Generic default: stream bytes to the client TCP
		s.ForwardToTCP(payload)
		return
	}

	// 3) Unknown/invalid
	log.Infof("Unhandled WS message: neither control nor framed-binary")
}
