package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Liveness and readiness are split: liveness failing means "restart me",
// readiness failing means "stop sending traffic". A database check in
// liveness would restart every replica on a database blip.
type health struct {
	ready   Readiness
	version string
}

func newHealth(ready Readiness, version string) *health {
	return &health{ready: ready, version: version}
}

// probeTimeout bounds the readiness database check; a probe that outlives
// its interval stacks connections against a struggling database.
const probeTimeout = 2 * time.Second

// Live reports that the process is running; it touches nothing. version lets
// an operator confirm which binary is running.
func (h *health) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": h.version,
	})
}

// Ready reports whether the process can serve requests, which for this
// service means the database answers.
func (h *health) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
	defer cancel()

	if err := h.ready.Ping(ctx); err != nil {
		// 503, not 500: a dependency is unavailable, not this service
		// broken, which tells the orchestrator to wait rather than restart.
		// "did not answer" covers slow/saturated as well as unreachable.
		// Not the apierr shape: this body answers kubectl, not the frontend.
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "database did not answer",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
