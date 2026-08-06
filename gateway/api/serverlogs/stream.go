package serverlogsapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/serverlogs"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	defaultBacklog       = serverlogs.Capacity
	streamPollInterval   = 1 * time.Second
	sseKeepaliveInterval = 30 * time.Second
)

// Stream Server Logs
//
//	@Summary		Stream Server Logs
//	@Description	Streams gateway and agent runtime logs in real-time via SSE. Sends a backlog of recent entries on connect, then each new entry as it is captured.
//	@Tags			Server Logs
//	@Produce		text/event-stream
//	@Param			backlog	query		int	false	"Number of buffered entries to replay on connect; defaults to and is capped at the in-memory buffer capacity (300), 0 disables"
//	@Success		200		{string}	string
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/server-logs/stream [get]
func Stream(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	backlog := parseIntQuery(c, "backlog", defaultBacklog, 0, maxLimit)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputils.AbortWithErr(c, http.StatusInternalServerError, fmt.Errorf("streaming not supported"), "streaming not supported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Commit response headers immediately so clients (fetch / EventSource)
	// resolve their connect promise without waiting for the first event.
	fmt.Fprint(c.Writer, ": connected\n\n")
	flusher.Flush()

	// Initial cursor and backlog, taken atomically from the ring.
	entries, seq := serverlogs.Since(ctx.OrgID, 0)
	if backlog > 0 {
		if err := writeEvents(c, flusher, lastN(toResponse(entries), backlog)); err != nil {
			return
		}
	}

	poll := time.NewTicker(streamPollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-poll.C:
			var fresh []serverlogs.Entry
			fresh, seq = serverlogs.Since(ctx.OrgID, seq)
			if len(fresh) == 0 {
				continue
			}
			if err := writeEvents(c, flusher, toResponse(fresh)); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEvents writes one SSE "log" event per entry and flushes once.
func writeEvents(c *gin.Context, flusher http.Flusher, entries []openapi.ServerLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		payload, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(c.Writer, "event: log\ndata: %s\n\n", payload); err != nil {
			// Debug: the handler returns right after, so this can never
			// recurse into its own dead stream; other subscribers see at
			// most one line per dead client, and only under LOG_LEVEL=debug.
			log.Debugf("sse: server logs client write failed: %v", err)
			return err
		}
	}
	flusher.Flush()
	return nil
}
