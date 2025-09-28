package transport

import (
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

	// Register this agent's communicator inside the broker
	if err := broker.CreateAgent(agent.Name, wsConn); err != nil {
		log.Errorf("failed to register agent communicator: %v", err)
		return
	}

	log.Infof("WebSocket connection established for agent=%s", agent.Name)

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
		handleWebSocketMessage(data)
	}

	// Cleanup: close all sessions that belong to this agent ===
	for id, s := range broker.GetSessions() {
		if s.Connection.AgentName == agent.Name {
			log.Debugf("Closing session %s due to agent=%s disconnect", id, agent.Name)
			s.Close()
		}
	}
}

func handleWebSocketMessage(data []byte) {
	// 1) Try CONTROL frame first (JSON-like)
	if sid, msg, err := broker.DecodeWebSocketMessage(data); err == nil {
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
