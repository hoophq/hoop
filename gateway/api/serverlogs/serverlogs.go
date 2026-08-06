package serverlogsapi

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/serverlogs"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// The limit clamp is derived from the ring capacity so the endpoint never
// advertises more history than the buffer holds. The default returns the
// full tail.
const (
	defaultLimit = serverlogs.Capacity
	maxLimit     = serverlogs.Capacity
)

// List Server Logs
//
//	@Summary		List Server Logs
//	@Description	Tail of recent in-memory runtime logs from the gateway process and connected agents
//	@Tags			Server Logs
//	@Produce		json
//	@Param			limit	query		int	false	"Max number of most recent entries; defaults to and is capped at the in-memory buffer capacity"
//	@Success		200		{array}		openapi.ServerLogEntry
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/server-logs [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	limit := parseIntQuery(c, "limit", defaultLimit, 1, maxLimit)
	c.JSON(http.StatusOK, lastN(toResponse(serverlogs.Snapshot(ctx.OrgID)), limit))
}

// toResponse maps buffered entries to the response schema, sorted by
// timestamp ascending (agent batches arrive with a small shipping delay, so
// ring order alone can interleave sources out of time order).
func toResponse(entries []serverlogs.Entry) []openapi.ServerLogEntry {
	out := make([]openapi.ServerLogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, openapi.ServerLogEntry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Message:   e.Message,
			Logger:    e.Logger,
			Fields:    e.Fields,
			Source:    e.Source,
			AgentID:   e.AgentID,
			AgentName: e.AgentName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

// lastN returns the trailing n entries (tail semantics), never nil.
func lastN(entries []openapi.ServerLogEntry, n int) []openapi.ServerLogEntry {
	if entries == nil {
		return []openapi.ServerLogEntry{}
	}
	if len(entries) > n {
		return entries[len(entries)-n:]
	}
	return entries
}

// parseIntQuery parses an integer query parameter, falling back to def on
// absence or parse error and clamping the result to [min, max].
func parseIntQuery(c *gin.Context, name string, def, min, max int) int {
	v := def
	if raw := c.Query(name); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			v = parsed
		}
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
