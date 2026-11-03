package liveeventsapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const keepaliveInterval = 30 * time.Second

// maxStreamAge ends a stream so the client reconnects and authenticates again.
var maxStreamAge = 30 * time.Minute

// browserEvent is the frame the client reads: the audience stays here.
type browserEvent struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Stream Live Events
//
//	@Summary		Stream Live Events
//	@Description	Streams change notices of the organization via SSE, one "event: <type>" frame per change, with {"type","id"} as data. A notice names what changed and never its content: read the resource again through its own route. Types: sidecar.checked_in (admins), review.created (users who can approve the review). The stream ends after 30 minutes; reconnect.
//	@Tags			Live Events
//	@Produce		text/event-stream
//	@Success		200	{string}	string
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/events/stream [get]
func Stream(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputils.AbortWithErr(c, http.StatusInternalServerError, fmt.Errorf("streaming not supported"), "streaming not supported")
		return
	}
	events, stop := liveevents.Subscribe(ctx.OrgID)
	defer stop()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	// Commits the headers, so the client's fetch resolves before the first
	// event.
	if _, err := fmt.Fprint(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	expire := time.NewTimer(maxStreamAge)
	defer expire.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-expire.C:
			return
		case ev := <-events:
			if !allowed(ctx, ev) {
				continue
			}
			payload, err := json.Marshal(browserEvent{Type: ev.Type, ID: ev.ID})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// allowed is the per-type access check. A type not listed serves an admin page.
func allowed(ctx *storagev2.Context, ev liveevents.Event) bool {
	switch ev.Type {
	case liveevents.ReviewCreated:
		return ev.Audience.Allows(ctx.UserID, ctx.UserGroups, ctx.IsAdmin())
	default:
		return ctx.IsAdmin()
	}
}
