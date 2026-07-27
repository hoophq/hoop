package mcpserver

import (
	"context"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newWaitHeartbeat builds a waitUntil tick callback that emits MCP progress
// notifications on the request's own POST stream while a long-running wait tool
// (reviews_wait, sessions_wait_analysis) polls.
//
// Why this exists: the streamable-HTTP POST that carries a wait tool call sits
// silent for as long as the wait runs. Intermediate proxies and some MCP
// clients tear down an HTTP request that produces no bytes for ~60-120s.
// Emitting a notification on that same request stream every poll interval keeps
// the connection alive. The notification rides ctx — the handler context the
// SDK tagged with the incoming request ID — so it is routed to this request's
// POST stream rather than the standalone SSE stream that the broken
// ServerOptions.KeepAlive used to ping (see mcpserver.go for that history).
//
// Progress notifications are part of the MCP spec and are only meaningful when
// the client opted in by sending a progressToken with the request. When no
// token is present we return nil and waitUntil runs without a heartbeat: the
// session still survives (it is held alive by the in-flight POST), and the wait
// simply relies on its timeout. We never send unsolicited progress
// notifications, which keeps the behavior spec-compliant across every client.
func newWaitHeartbeat(ctx context.Context, req *mcp.CallToolRequest, total time.Duration) func(elapsed time.Duration) {
	if req == nil || req.Session == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}

	ss := req.Session
	totalSeconds := total.Seconds()
	return func(elapsed time.Duration) {
		// Best-effort: a heartbeat delivery failure must never abort the wait,
		// so we only log it. Progress is reported in seconds elapsed, which is
		// monotonically increasing as the spec requires.
		err := ss.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      elapsed.Seconds(),
			Total:         totalSeconds,
			Message:       "waiting for completion",
		})
		if err != nil {
			log.Debugf("mcp: wait heartbeat notification failed: %v", err)
		}
	}
}
