package transport

import (
	"encoding/json"
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
			log.Errorf("WebSocket read error: %v", err)
			break
		}

		if messageType == websocket.BinaryMessage {
			handleWebSocketMessage(data)
		}
	}

}

// handleWebSocketMessage handles incoming WebSocket messages
func handleWebSocketMessage(data []byte) {
	// Try to decode as header + JSON message
	if header, headerLen, err := broker.DecodeHeader(data); err == nil && headerLen <= len(data) {
		session := broker.GetSession(header.SID)

		if session == nil {
			log.Printf("No session found for SID: %s", header.SID)
			return
		}

		jsonData := data[headerLen:]
		if len(jsonData) > 0 {
			var response map[string]interface{}
			if err := json.Unmarshal(jsonData, &response); err == nil {
				// Check if it's an RDP started response
				// TODO improve this to by defining message types (RDP, SSH,... etc) in the future
				if messageType, ok := response["message_type"].(string); ok && messageType == "rdp_started" {
					log.Printf("Received RDP started response for session: %s", header.SID)
					session.AcknowledgeCredentials()
					return
				}
			}
		}

		// Forward RDP data to TCP session
		rdpData := data[headerLen:]
		session.ForwardToTCP(rdpData)
	}
}
