package ws

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

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

func validateTokenWs(token string) (*models.Agent, error) {
	dsn, err := dsnkeys.Parse(token)
	if err != nil {
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, agent-token=%v, err=%v", len(token), token, err)
		log.With("token_length", len(token)).Errorf("invalid agent authentication (dsn), err=%v", err)
		return nil, fmt.Errorf("invalid authentication")
	}

	ag, err := models.GetAgentByToken(dsn.SecretKeyHash)
	if err != nil {
		log.Debugf("invalid agent authentication (dsn), tokenlength=%v, agent-token=%v, err=%v", len(token), token, err)
		return nil, fmt.Errorf("invalid authentication")
	}
	if ag.Name != dsn.Name {
		log.Errorf("failed authenticating agent (agent dsn), mismatch dsn attributes. id=%v, name=%v, mode=%v",
			ag.ID, dsn.Name, dsn.AgentMode)
		return nil, fmt.Errorf("invalid authentication, mismatch dsn attributes")
	}
	return ag, nil
}

func HandleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	defer conn.Close()
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	// this could be a ws middleware
	key := c.Request.Header.Get("HOOP_KEY")

	if key == "" {
		errMsg := "missing HOOP_KEY header"
		log.Errorf(errMsg)
		conn.WriteMessage(websocket.TextMessage, []byte(errMsg))
		conn.Close()
		return
	}
	validateAgent, err := validateTokenWs(key)
	if err != nil {
		errMsg := "invalid authentication"
		log.Errorf("%v: %v", errMsg, err)
		conn.WriteMessage(websocket.TextMessage, []byte(errMsg))
		conn.Close()
		return
	}
	fmt.Printf("Agent %v connected via WebSocket\n", validateAgent.Name)

	broker.CreateAgent(validateAgent.Name, conn)

	log.Println("WebSocket connection established")

	// Handle incoming messages
	client, found := broker.GetAgent(validateAgent.Name)
	if !found || client == nil {
		log.Printf("Agent not found or nil")
		return
	}

	for {
		messageType, data, err := client.Connection.(*websocket.Conn).ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		if messageType == websocket.BinaryMessage {
			handleWebSocketMessage(data)
		}
	}

	log.Println("WebSocket connection closed")
}

// handleWebSocketMessage handles incoming WebSocket messages
func handleWebSocketMessage(data []byte) {
	log.Printf("Received %d bytes from agent", len(data))

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
