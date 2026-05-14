package sessionapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/eventbroker"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const sseKeepaliveInterval = 30 * time.Second

type sessionStreamEvent struct {
	Time    string `json:"time"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// StreamSession streams session events via Server-Sent Events.
//
//	@Summary		Stream Session Events
//	@Description	Streams audit events for a machine session in real-time via SSE. Each event is published as it is appended to the WAL. No catch-up is sent for events that occurred before the subscription.
//	@Tags			Sessions
//	@Produce		text/event-stream
//	@Param			session_id		path	string	true	"The session ID"
//	@Success		200				{string}	string
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/stream [get]
func StreamSession(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	if errors.Is(err, models.ErrNotFound) {
		httputils.AbortWithErr(c, http.StatusNotFound, err, "session not found")
		return
	}
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}
	if !canAccessSession(ctx, session) {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
		return
	}
	if session.IdentityType != "machine" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "streaming is only supported for machine sessions"})
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputils.AbortWithErr(c, http.StatusInternalServerError, fmt.Errorf("streaming not supported"), "streaming not supported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// short-circuit completed sessions: nothing live to subscribe to
	if session.Status == string(openapi.SessionStatusDone) {
		fmt.Fprint(c.Writer, "event: session_end\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	ch, unsubscribe := eventbroker.Default.Subscribe(sessionID)
	defer unsubscribe()

	// re-check after subscribing to close the race where the session ended
	// between the first status check and Subscribe
	session, err = models.GetSessionByID(ctx.OrgID, sessionID)
	if err == nil && session.Status == string(openapi.SessionStatusDone) {
		fmt.Fprint(c.Writer, "event: session_end\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				fmt.Fprint(c.Writer, "event: session_end\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			payload, _ := json.Marshal(sessionStreamEvent{
				Time:    ev.Time.UTC().Format(time.RFC3339Nano),
				Type:    ev.Type,
				Payload: base64.StdEncoding.EncodeToString(ev.Payload),
			})
			if _, err := fmt.Fprintf(c.Writer, "event: event\ndata: %s\n\n", payload); err != nil {
				log.With("sid", sessionID).Debugf("sse: client write failed: %v", err)
				return
			}
			flusher.Flush()
		case <-c.Request.Context().Done():
			return
		case <-time.After(sseKeepaliveInterval):
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
