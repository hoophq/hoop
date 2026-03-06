package rdp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var replayUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16384,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// ReplayHandler handles WebSocket connections for RDP session replay
// It reads the recorded session from the database and streams the output events
// back to an IronRDP web client for playback.
//
// The replay process:
// 1. Wait for client's initial RDCleanPathPdu request
// 2. Send recorded handshake response (RDCleanPathPdu with ServerCertChain and X224ConnectionPDU)
// 3. Stream recorded output events (bitmap updates, etc.) to the client
func ReplayHandler(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	log.With("sid", sessionID).Infof("RDP replay request from user=%s", ctx.UserEmail)

	// Get the session from database
	session, err := models.GetSessionByID("104adf14-7a72-4c15-a9c3-607ae25f64ab", sessionID)
	if err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
			return
		}
		log.With("sid", sessionID).Errorf("failed to fetch session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to fetch session"})
		return
	}
	/*
		// Check if user has access to this session
		if session.UserID != ctx.UserID && !ctx.IsAuditorOrAdminUser() {
			c.JSON(http.StatusForbidden, gin.H{"message": "access denied"})
			return
		}

		// Verify this is an RDP session
		if session.ConnectionSubtype != "rdp" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "not an RDP session"})
			return
		}
	*/
	// Get the blob stream
	blob, err := session.GetBlobStream()
	if err != nil {
		log.With("sid", sessionID).Errorf("failed to fetch blob stream: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to fetch session data"})
		return
	}

	if blob == nil || len(blob.BlobStream) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "no session data available for replay"})
		return
	}

	// Parse the event stream
	var events []rdpEvent
	if err := json.Unmarshal(blob.BlobStream, &events); err != nil {
		log.With("sid", sessionID).Errorf("failed to parse event stream: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to parse session data"})
		return
	}

	// Extract handshake data and output events
	var handshakeData []byte
	var outputEvents []rdpEvent
	for _, event := range events {
		eventType, _ := event[1].(string)
		if eventType == "h" {
			// Handshake event - decode it
			eventDataB64, _ := event[2].(string)
			handshakeData, _ = base64.StdEncoding.DecodeString(eventDataB64)
		} else if eventType == "o" {
			// Output event (server -> client data, which contains bitmaps)
			outputEvents = append(outputEvents, event)
		}
	}

	if len(handshakeData) == 0 {
		log.With("sid", sessionID).Warnf("no handshake data found in session")
		c.JSON(http.StatusNotFound, gin.H{"message": "no handshake data available for replay"})
		return
	}

	// Upgrade to WebSocket
	ws, err := replayUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.With("sid", sessionID).Errorf("failed to upgrade websocket: %v", err)
		return
	}
	defer ws.Close()

	log.With("sid", sessionID).Infof("starting RDP replay, output events=%d", len(outputEvents))

	// Phase 1: Handshake
	// Wait for client's initial RDCleanPathPdu request
	_, msg, err := ws.ReadMessage()
	if err != nil {
		log.With("sid", sessionID).Errorf("failed to read client handshake: %v", err)
		return
	}

	// Parse the client's handshake to get version
	var clientPdu RDCleanPathPdu
	if err := unmarshalContextExplicit(msg, &clientPdu); err != nil {
		log.With("sid", sessionID).Errorf("failed to parse client handshake: %v", err)
		return
	}

	log.With("sid", sessionID).Debugf("received client handshake, version=%s", clientPdu.Version)

	// Send the recorded handshake response
	if err := ws.WriteMessage(websocket.BinaryMessage, handshakeData); err != nil {
		log.With("sid", sessionID).Errorf("failed to send handshake response: %v", err)
		return
	}

	log.With("sid", sessionID).Debugf("handshake completed, streaming output events")

	// Phase 2: Stream output events to the client with proper timing
	for i, event := range outputEvents {
		// event[0] = timestamp (seconds), event[1] = type, event[2] = base64 data
		eventTime, _ := event[0].(float64)
		eventDataB64, _ := event[2].(string)

		// Calculate when this event should be sent (relative timing)
		if i > 0 {
			prevEventTime, _ := outputEvents[i-1][0].(float64)
			delay := time.Duration((eventTime - prevEventTime) * float64(time.Second))
			// Cap delay to prevent long waits, but allow some timing for realistic replay
			if delay > 0 && delay < 5*time.Second {
				time.Sleep(delay)
			}
		}

		// Decode the base64 data
		eventData, err := base64.StdEncoding.DecodeString(eventDataB64)
		if err != nil {
			log.With("sid", sessionID).Warnf("failed to decode event data at index %d: %v", i, err)
			continue
		}

		// Send the data to the client
		if err := ws.WriteMessage(websocket.BinaryMessage, eventData); err != nil {
			log.With("sid", sessionID).Errorf("failed to write message: %v", err)
			break
		}
	}

	log.With("sid", sessionID).Infof("RDP replay completed")
}
