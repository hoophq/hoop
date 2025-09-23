package ws

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/gateway/broker"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

func HandleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	//TODO I need to get the hoop_key from a agent header
	// parse this and get the agent information from the hoop_key
	// c.Request.Header.Get("HOOP_KEY")
	// for example get the hoop_key id
	// hoop_key -> agent id -> 1

	defer conn.Close()
	// check  if there is a agent already running
	// before create
	broker.CreateAgent("1", conn)

	log.Println("WebSocket connection established")

	// Handle incoming messages
	client, found := broker.GetAgent("1")
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
