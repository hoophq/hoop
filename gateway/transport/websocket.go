package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		//TODO : improve origin check for future version
		// Allow all connections by default
		return true
	},
}

// validate hoop key from coonectoin websocket
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
		return nil, err
	}

	return ag, nil
}

func HandlerSocket(c *gin.Context) {
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
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	defer conn.Close()
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	broker.CreateAgent(agent.Name, conn)

	log.Println("WebSocket connection established")

	// Handle incoming messages
	client, found := broker.GetAgent(agent.Name)
	if !found || client == nil {
		log.Printf("Agent not found or nil")
		return
	}

	conn, ok := client.Connection.(*websocket.Conn)
	if !ok {
		log.Errorf("Invalid WebSocket connection type")
		return
	}

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			// Check if it's a normal closure or EOF
			//TODO this can be just agent is down or shutdown
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Debugf("WebSocket connection closed normally: %v", err)
			} else if err.Error() == "EOF" {
				log.Debugf("WebSocket connection closed by client (EOF)")
			} else {
				log.Errorf("WebSocket read error: %v", err)
			}
			break
		}

		if messageType == websocket.BinaryMessage {
			handleWebSocketMessage(data)
		}
	}

	// Cleanup: Close all sessions for this agent when WebSocket disconnects
	sessions := broker.GetSessions()
	for sessionID, session := range sessions {
		if session.Agent != nil {
			if wsConn, ok := session.Agent.Connection.(*websocket.Conn); ok && wsConn == conn {
				log.Debugf("Closing session %s due to agent disconnect", sessionID)
				session.Close()
			}
		}
	}

}

// handleWebSocketMessage handles incoming WebSocket messages
func handleWebSocketMessage(data []byte) {
	// Try to decode as WebSocketMessage first (for control messages)
	if _, _, err := broker.DecodeWebSocketMessage(data); err == nil {
		// This is a control message, handle it normally
		broker.HandleWebSocketMessage(data)
	} else {
		// This might be raw RDP data, try to decode as raw data with header
		if header, headerLen, err := broker.DecodeHeader(data); err == nil {
			if len(data) >= headerLen {
				// This is raw RDP data, forward it to the session
				session := broker.GetSession(header.SID)
				if session != nil {
					rdpData := data[headerLen:]
					session.ForwardToTCP(rdpData)
				} else {
					log.Printf("No session found for raw RDP data, SID: %s", header.SID)
				}
			} else {
				log.Printf("Insufficient data for raw RDP payload")
			}
		} else {
			log.Printf("Failed to decode message as WebSocketMessage or raw data: %v", err)
		}
	}
}
