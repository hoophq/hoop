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

// Stream Live Events
//
//	@Summary		Stream Live Events
//	@Description	Streams change notices of the organization via SSE, one "event: <type>" frame per change, with {"type","id"} as data. A notice names what changed and never its content: read the resource again through its own route. Types: sidecar.checked_in.
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
	fmt.Fprint(c.Writer, ": connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev := <-events:
			payload, err := json.Marshal(ev)
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
