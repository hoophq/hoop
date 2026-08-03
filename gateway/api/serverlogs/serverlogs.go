package serverlogsapi

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/serverlogs"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	defaultLimit = 500
	maxLimit     = 5000
)

// List Server Logs
//
//	@Summary		List Server Logs
//	@Description	Tail of recent in-memory runtime logs from the gateway process and connected agents
//	@Tags			Server Logs
//	@Produce		json
//	@Param			limit	query		int	false	"Max number of most recent entries (default 500, max 5000)"
//	@Success		200		{array}		openapi.ServerLogEntry
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/server-logs [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	limit := parseIntQuery(c, "limit", defaultLimit, 1, maxLimit)
	entries := merged(log.Tail.Snapshot(), serverlogs.AgentSnapshot(ctx.OrgID))
	c.JSON(http.StatusOK, lastN(entries, limit))
}

// merged wraps gateway and agent tail entries into the response schema and
// sorts the combined slice by timestamp ascending.
func merged(gw []log.TailEntry, ag []serverlogs.AgentLogEntry) []openapi.ServerLogEntry {
	out := make([]openapi.ServerLogEntry, 0, len(gw)+len(ag))
	for _, e := range gw {
		out = append(out, openapi.ServerLogEntry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Message:   e.Message,
			Logger:    e.Logger,
			Fields:    e.Fields,
			Source:    "gateway",
		})
	}
	for _, e := range ag {
		out = append(out, openapi.ServerLogEntry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Message:   e.Message,
			Logger:    e.Logger,
			Fields:    e.Fields,
			Source:    "agent",
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
