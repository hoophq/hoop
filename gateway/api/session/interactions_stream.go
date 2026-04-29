package sessionapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/interactionbroker"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	sseKeepaliveInterval = 30 * time.Second
	streamFetchLimit     = 100
)

type sessionEndEvent struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// StreamInteractions streams session interactions via Server-Sent Events.
//
//	@Summary		Stream Session Interactions
//	@Description	Streams interactions for a machine session in real-time via SSE. Sends existing interactions as catch-up, then pushes new ones as they are created.
//	@Tags			Sessions
//	@Produce		text/event-stream
//	@Param			session_id		path	string	true	"The session ID"
//	@Param			after_sequence	query	int		false	"Only stream interactions with sequence > N"
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/interactions/stream [get]
func StreamInteractions(c *gin.Context) {
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
		c.JSON(http.StatusBadRequest, gin.H{"message": "streaming interactions is only supported for machine sessions"})
		return
	}

	afterSequence, _ := strconv.Atoi(c.DefaultQuery("after_sequence", "0"))

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputils.AbortWithErr(c, http.StatusInternalServerError, fmt.Errorf("streaming not supported"), "streaming not supported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	sessionDone := session.Status == string(openapi.SessionStatusDone)

	if sessionDone {
		lastSeq := sendCatchUp(c, flusher, ctx.OrgID, sessionID, afterSequence)
		writeSessionEnd(c, flusher, sessionID, session.Status)
		log.With("sid", sessionID).Infof("sse: session already done, sent catch-up (last_seq=%d)", lastSeq)
		return
	}

	ch, unsubscribe := interactionbroker.Default.Subscribe(sessionID)
	defer unsubscribe()

	lastSequence := sendCatchUp(c, flusher, ctx.OrgID, sessionID, afterSequence)

	// re-check session status after subscribing to handle the race
	// where the session ended between the initial check and subscribe
	session, err = models.GetSessionByID(ctx.OrgID, sessionID)
	if err == nil && session.Status == string(openapi.SessionStatusDone) {
		lastSequence = sendCatchUp(c, flusher, ctx.OrgID, sessionID, lastSequence)
		writeSessionEnd(c, flusher, sessionID, session.Status)
		return
	}

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				lastSequence = sendCatchUp(c, flusher, ctx.OrgID, sessionID, lastSequence)
				writeSessionEnd(c, flusher, sessionID, string(openapi.SessionStatusDone))
				return
			}
			switch event.Type {
			case interactionbroker.InteractionCreated:
				lastSequence = sendCatchUp(c, flusher, ctx.OrgID, sessionID, lastSequence)
			case interactionbroker.SessionEnded:
				lastSequence = sendCatchUp(c, flusher, ctx.OrgID, sessionID, lastSequence)
				writeSessionEnd(c, flusher, sessionID, string(openapi.SessionStatusDone))
				return
			}
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

func sendCatchUp(c *gin.Context, flusher http.Flusher, orgID, sessionID string, afterSequence int) int {
	lastSequence := afterSequence
	for {
		interactions, err := models.ListSessionInteractions(models.DB, orgID, sessionID, lastSequence, streamFetchLimit)
		if err != nil {
			log.With("sid", sessionID).Errorf("sse: failed listing interactions: %v", err)
			return lastSequence
		}
		for _, interaction := range interactions {
			item, err := serializeInteraction(orgID, interaction)
			if err != nil {
				log.With("sid", sessionID).Errorf("sse: failed serializing interaction: %v", err)
				return lastSequence
			}
			data, _ := json.Marshal(item)
			if _, err := fmt.Fprintf(c.Writer, "event: interaction\nid: %d\ndata: %s\n\n", interaction.Sequence, data); err != nil {
				return lastSequence
			}
			flusher.Flush()
			lastSequence = interaction.Sequence
		}
		if len(interactions) < streamFetchLimit {
			break
		}
	}
	return lastSequence
}

func writeSessionEnd(c *gin.Context, flusher http.Flusher, sessionID, status string) {
	data, _ := json.Marshal(sessionEndEvent{SessionID: sessionID, Status: status})
	fmt.Fprintf(c.Writer, "event: session_end\ndata: %s\n\n", data)
	flusher.Flush()
}
