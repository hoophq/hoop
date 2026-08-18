package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

var upgrader = websocket.Upgrader{
	//ReadBufferSize:  32 << 10, // 32 KiB (tune as needed)
	//WriteBufferSize: 32 << 10,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: tighten origin checks later
		return true
	},
	EnableCompression: false,
}

const (
	// ReadMessage allocates a complete WebSocket message. Bound that allocation
	// before the per-session relay queue gets a chance to admit the payload.
	maxAgentWebSocketMessageBytes = broker.HeaderSize + 32<<20
	maxAgentControlFrameBytes     = 1 << 20
	maxAgentGuardrailsPayload     = 256 << 10
	maxAgentGuardrailsDetections  = 4096
	agentGuardrailsQueueSlots     = 8
	agentGuardrailsWorkers        = 2
	agentGuardrailsPersistTimeout = 15 * time.Second
)

var errAgentGuardrailsQueueFull = errors.New("agent guardrails persistence queue full")

func verifyWebsocketToken(token string) (*models.Agent, error) {
	dsn, err := dsnkeys.Parse(token)
	if err != nil {
		log.With("token_length", len(token)).Errorf("invalid agent authentication (dsn), err=%v", err)
		return nil, err
	}

	ag, err := models.GetAgentByToken(dsn.SecretKeyHash)
	if err != nil {
		log.With("token_length", len(token)).Debugf("invalid agent authentication (dsn), err=%v", err)
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
	wsConn.SetReadLimit(int64(maxAgentWebSocketMessageBytes))

	// Register this agent's communicator inside the broker. The returned
	// instance id ties cleanup to THIS connection so a late close of a stale
	// connection cannot evict a newer one that reused the same agent name.
	agentInstanceID, err := broker.CreateAgent(agent.Name, wsConn)
	if err != nil {
		log.Errorf("failed to register agent communicator: %v", err)
		return
	}
	reportProcessor := newAgentGuardrailsProcessor()

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
		if err := handleWebSocketMessage(agent.Name, agentInstanceID, data, reportProcessor); err != nil {
			log.With("agent", agent.Name).Warnf("closing agent WebSocket after protocol failure: %v", err)
			break
		}
	}

	// Stop this connection's sessions first. Session.Close retains their
	// immutable audit routes, so already-admitted reports remain persistable.
	// Drain the fixed worker pool while this exact agent communicator is still
	// registered, allowing successful transactions to be acknowledged.
	broker.CloseAgentInstanceSessions(agentInstanceID)
	reportProcessor.closeAndWait()

	// Instance-scoped removal cannot evict a same-name replacement connection.
	broker.RemoveAgent(agent.Name, agentInstanceID)
}

// agentGuardrailsViolation mirrors the Rust agent's ViolationReport
// (libhoop rdp-guard/src/piigate/report.rs). Entity metadata only — no
// pixels or recognized text.
type agentGuardrailsViolation struct {
	Kind         string                     `json:"kind"` // "detection" | "overload" | "analysis_error"
	EntityTypes  []string                   `json:"entity_types"`
	Detections   []agentGuardrailsDetection `json:"detections"`
	DroppedBytes int                        `json:"dropped_bytes"`
}

type agentGuardrailsDetection struct {
	EntityType string  `json:"entity_type"`
	Score      float64 `json:"score"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
}

type agentGuardrailsWork struct {
	agentName     string
	agentInstance uuid.UUID
	sid           uuid.UUID
	route         *broker.SessionAuditRoute
	reportID      string
	report        agentGuardrailsViolation
}

// agentGuardrailsProcessor owns a fixed worker set for one agent connection.
// Work admission is nonblocking; a full queue is a protocol failure that
// closes the offending connection rather than spawning unbounded goroutines.
type agentGuardrailsProcessor struct {
	queue chan agentGuardrailsWork
	wg    sync.WaitGroup
}

func newAgentGuardrailsProcessor() *agentGuardrailsProcessor {
	processor := &agentGuardrailsProcessor{
		queue: make(chan agentGuardrailsWork, agentGuardrailsQueueSlots),
	}
	processor.wg.Add(agentGuardrailsWorkers)
	for range agentGuardrailsWorkers {
		go func() {
			defer processor.wg.Done()
			for work := range processor.queue {
				processAgentGuardrailsWork(work)
			}
		}()
	}
	return processor
}

func (p *agentGuardrailsProcessor) submit(work agentGuardrailsWork) error {
	select {
	case p.queue <- work:
		return nil
	default:
		return errAgentGuardrailsQueueFull
	}
}

func (p *agentGuardrailsProcessor) closeAndWait() {
	close(p.queue)
	p.wg.Wait()
}

func processAgentGuardrailsWork(work agentGuardrailsWork) {
	ctx, cancel := context.WithTimeout(context.Background(), agentGuardrailsPersistTimeout)
	defer cancel()
	if err := persistAgentGuardrailsViolation(
		ctx,
		work.route,
		work.reportID,
		work.report,
	); err != nil {
		log.With("sid", work.sid.String()).Warnf(
			"piigate: failed to persist agent guardrails violation: %v",
			err,
		)
		return
	}
	if err := acknowledgeAgentGuardrailsViolation(
		work.agentName,
		work.agentInstance,
		work.sid,
		work.reportID,
	); err != nil {
		log.With("sid", work.sid.String()).Warnf(
			"piigate: failed to acknowledge agent guardrails violation: %v",
			err,
		)
	}
}

func decodeAgentGuardrailsViolation(payload []byte) (agentGuardrailsViolation, error) {
	if len(payload) > maxAgentGuardrailsPayload {
		return agentGuardrailsViolation{}, fmt.Errorf(
			"agent guardrails payload exceeds %d bytes",
			maxAgentGuardrailsPayload,
		)
	}
	var report agentGuardrailsViolation
	if err := json.Unmarshal(payload, &report); err != nil {
		return agentGuardrailsViolation{}, fmt.Errorf("decode agent guardrails violation: %w", err)
	}
	if len(report.Detections) > maxAgentGuardrailsDetections {
		return agentGuardrailsViolation{}, fmt.Errorf(
			"agent guardrails detection count exceeds %d",
			maxAgentGuardrailsDetections,
		)
	}
	switch report.Kind {
	case "detection", "overload", "analysis_error":
	default:
		return agentGuardrailsViolation{}, fmt.Errorf(
			"unsupported agent guardrails violation kind %q",
			report.Kind,
		)
	}
	return report, nil
}

const agentGuardrailsReportIDKey = "report_id"

func agentGuardrailsDatabaseSessionID(route *broker.SessionAuditRoute) (string, bool) {
	if route == nil {
		return "", false
	}
	return route.DatabaseSessionID, route.DatabaseSessionID != ""
}

// persistAgentGuardrailsViolation records one idempotent report. The model
// locks the database session row and commits guardrails_info plus detections in
// one transaction; an acknowledgement is sent only after this returns nil.
func persistAgentGuardrailsViolation(
	ctx context.Context,
	route *broker.SessionAuditRoute,
	reportID string,
	report agentGuardrailsViolation,
) error {
	sessionID, ok := agentGuardrailsDatabaseSessionID(route)
	if !ok || route.OrgID == "" {
		return fmt.Errorf("agent guardrails violation has no durable session route")
	}

	ruleType := ""
	switch report.Kind {
	case "detection":
		ruleType = "pii_detection"
	case "overload":
		ruleType = "pii_guard_overload"
	case "analysis_error":
		ruleType = "pii_guard_analysis_error"
	default:
		return fmt.Errorf("unsupported agent guardrails violation kind %q", report.Kind)
	}
	info := []models.SessionGuardRailsInfo{{
		ReportID:     reportID,
		RuleName:     "rdp_pii_guard",
		Rule:         models.SessionGuardRailMatchedRule{Type: ruleType},
		Direction:    "server_to_client",
		MatchedWords: report.EntityTypes,
	}}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal agent guardrails info: %w", err)
	}

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
	if err := services.PersistRDPGuardrailViolation(
		ctx,
		models.DB,
		route.OrgID,
		sessionID,
		reportID,
		data,
		detections,
	); err != nil {
		return fmt.Errorf("persist agent guardrails violation: %w", err)
	}

	log.With("sid", sessionID).Infof(
		"piigate: agent-side PII guard violation persisted (report_id=%s, kind=%s, entities=%v)",
		reportID,
		report.Kind,
		report.EntityTypes,
	)
	return nil
}

func acknowledgeAgentGuardrailsViolation(
	agentName string,
	agentInstanceID uuid.UUID,
	sid uuid.UUID,
	reportID string,
) error {
	ack := &broker.WebSocketMessage{
		Type: broker.MessageTypeGuardrailsViolationAck,
		Metadata: map[string]string{
			agentGuardrailsReportIDKey: reportID,
		},
		Payload: []byte{},
	}
	framed, err := broker.EncodeWebSocketMessageForAgent(
		agentName,
		agentInstanceID,
		sid,
		ack,
	)
	if err != nil {
		return fmt.Errorf("encode guardrails acknowledgement: %w", err)
	}
	agent, ok := broker.GetAgentInstance(agentName, agentInstanceID)
	if !ok {
		return fmt.Errorf("agent connection instance is no longer active")
	}
	return agent.Send(framed)
}

func acknowledgeAgentFrameProtocol(
	agentName string,
	agentInstanceID uuid.UUID,
) error {
	ack := &broker.WebSocketMessage{
		Type: broker.MessageTypeCapabilities,
		Metadata: map[string]string{
			broker.CapabilityFrameProtocol: broker.FrameProtocolV2,
		},
		Payload: []byte{},
	}
	framed, err := broker.EncodeWebSocketMessage(broker.ControlSentinelSID, ack)
	if err != nil {
		return fmt.Errorf("encode frame protocol acknowledgement: %w", err)
	}
	agent, ok := broker.GetAgentInstance(agentName, agentInstanceID)
	if !ok {
		return fmt.Errorf("agent connection instance is no longer active")
	}
	if err := agent.Send(framed); err != nil {
		return fmt.Errorf("send frame protocol acknowledgement: %w", err)
	}
	return nil
}

func decodeAgentControlFrame(
	agentName string,
	agentInstanceID uuid.UUID,
	header *broker.Header,
	payload []byte,
) (broker.WebSocketMessage, bool, error) {
	if header.Control {
		if len(payload) > maxAgentControlFrameBytes {
			return broker.WebSocketMessage{}, false, fmt.Errorf(
				"agent control frame exceeds %d bytes",
				maxAgentControlFrameBytes,
			)
		}
		var msg broker.WebSocketMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return broker.WebSocketMessage{}, false, fmt.Errorf(
				"decode agent control frame: %w",
				err,
			)
		}
		return msg, true, nil
	}
	if broker.AgentUsesFrameProtocolV2(agentName, agentInstanceID) ||
		len(payload) > maxAgentControlFrameBytes {
		return broker.WebSocketMessage{}, false, nil
	}

	// Pre-v2 agents omitted the frame-kind bit. Retain a bounded,
	// direction-aware decoder until this exact connection negotiates v2.
	// Invalid or unexpected JSON is raw target data, never a relay failure.
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return broker.WebSocketMessage{}, false, nil
	}
	var msg broker.WebSocketMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return broker.WebSocketMessage{}, false, nil
	}
	if header.SID == broker.ControlSentinelSID {
		if msg.Type != broker.MessageTypeCapabilities {
			return broker.WebSocketMessage{}, false, nil
		}
	} else {
		switch msg.Type {
		case broker.MessageTypeSessionStarted,
			broker.MessageTypeData,
			broker.MessageTypeGuardrailsViolation:
		default:
			return broker.WebSocketMessage{}, false, nil
		}
	}
	return msg, true, nil
}

func handleWebSocketMessage(
	agentName string,
	agentInstanceID uuid.UUID,
	data []byte,
	reportProcessor *agentGuardrailsProcessor,
) error {
	header, payload, err := broker.DecodeFrame(data)
	if err != nil {
		return fmt.Errorf("invalid agent frame: %w", err)
	}

	msg, control, err := decodeAgentControlFrame(
		agentName,
		agentInstanceID,
		header,
		payload,
	)
	if err != nil {
		return err
	}
	if control {
		// v2 acknowledgement is written before the capability is published.
		// A concurrent session therefore cannot send a typed control frame
		// before the agent has seen the mode transition.
		if header.SID == broker.ControlSentinelSID {
			switch msg.Type {
			case broker.MessageTypeCapabilities:
				if msg.Metadata[broker.CapabilityFrameProtocol] == broker.FrameProtocolV2 {
					if err := acknowledgeAgentFrameProtocol(agentName, agentInstanceID); err != nil {
						return err
					}
				}
				broker.SetAgentCapabilities(agentName, agentInstanceID, msg.Metadata)
				log.Debugf("agent=%s advertised capabilities: %v", agentName, msg.Metadata)
			default:
				log.Infof(
					"Unhandled connection-scoped control message type=%q for agent=%s",
					msg.Type,
					agentName,
				)
			}
			return nil
		}

		if msg.Type == broker.MessageTypeGuardrailsViolation {
			reportID := msg.Metadata[agentGuardrailsReportIDKey]
			parsedReportID, err := uuid.Parse(reportID)
			if err != nil || parsedReportID == uuid.Nil {
				return fmt.Errorf("invalid agent guardrails report_id %q", reportID)
			}
			reportID = parsedReportID.String()
			route := broker.GetSessionAuditRoute(header.SID)
			if route == nil || route.AgentInstanceID != agentInstanceID {
				return fmt.Errorf(
					"no matching teardown-safe audit route for agent violation SID=%s",
					header.SID,
				)
			}
			report, err := decodeAgentGuardrailsViolation(msg.Payload)
			if err != nil {
				return err
			}
			if err := reportProcessor.submit(agentGuardrailsWork{
				agentName:     agentName,
				agentInstance: agentInstanceID,
				sid:           header.SID,
				route:         route,
				reportID:      reportID,
				report:        report,
			}); err != nil {
				return err
			}
			return nil
		}

		session := broker.GetSessionForAgentInstance(header.SID, agentInstanceID)
		if session == nil {
			log.Infof("Control message for unknown or foreign SID=%s", header.SID)
			return nil
		}
		handler, ok := broker.ProtocolManagerInstance.GetHandler(session.Protocol)
		if !ok {
			log.Infof("No protocol handler for %q (SID=%s)", session.Protocol, header.SID)
			return nil
		}

		switch msg.Type {
		case broker.MessageTypeSessionStarted:
			_ = handler.HandleSessionStarted(session, &msg)
		case broker.MessageTypeData:
			if err := handler.HandleData(session, &msg); err != nil {
				log.With("sid", header.SID.String()).Warnf(
					"terminating session after relay admission failure: %v",
					err,
				)
				session.Close()
			}
		default:
			log.Infof("Unhandled control message type=%q for SID=%s", msg.Type, header.SID)
		}
		return nil
	}
	if header.SID == broker.ControlSentinelSID {
		return fmt.Errorf("connection-scoped frame is missing the control flag")
	}

	// Raw relay payload. DecodeFrame already proved Header.Len matches exactly.
	session := broker.GetSessionForAgentInstance(header.SID, agentInstanceID)
	if session == nil {
		log.Infof("Binary payload for unknown or foreign SID=%s", header.SID)
		return nil
	}

	if handler, ok := broker.ProtocolManagerInstance.GetHandler(session.Protocol); ok {
		message := &broker.WebSocketMessage{
			Type:     broker.MessageTypeData,
			Metadata: map[string]string{"transport": "binary"},
			Payload:  payload,
		}
		if err := handler.HandleData(session, message); err != nil {
			log.With("sid", header.SID.String()).Warnf(
				"terminating session after relay admission failure: %v",
				err,
			)
			session.Close()
		}
		return nil
	}

	if err := session.ForwardToTCP(payload); err != nil {
		log.With("sid", header.SID.String()).Warnf(
			"terminating session after relay admission failure: %v",
			err,
		)
		session.Close()
	}
	return nil
}
