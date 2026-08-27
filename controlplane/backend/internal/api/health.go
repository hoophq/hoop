package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/hoophq/hoop/controlplane/backend/internal/database"
)

// Liveness and readiness are split because they mean opposite things to an
// orchestrator. Liveness failing says "restart me". Readiness failing says
// "keep me running but stop sending traffic". Wiring a database check into
// liveness is the classic mistake: a database blip then restarts every
// replica at once, which turns a recoverable dependency failure into an
// outage of the thing that depends on it.
type health struct {
	db      *gorm.DB
	version string
}

func newHealth(db *gorm.DB, version string) *health {
	return &health{db: db, version: version}
}

// probeTimeout bounds the readiness database check.
//
// Two seconds, comfortably inside a typical probe interval. A readiness
// probe that blocks longer than the interval stacks up connections against a
// database that is already struggling, which is the failure it exists to
// report.
const probeTimeout = 2 * time.Second

// Live reports that the process is running. It touches nothing.
//
// version is here so an operator can confirm which binary is actually running
// without shelling into the container.
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

	if err := database.Ping(ctx, h.db); err != nil {
		// 503 rather than 500. This is a dependency being unavailable, not
		// this service being broken, and the distinction is what tells an
		// orchestrator to wait rather than to restart.
		//
		// "did not answer" rather than "not reachable": the same failure
		// fires when the database is merely slow or the pool is saturated,
		// and a reason naming the wrong cause sends the operator to check
		// the network.
		//
		// Not the apierr shape. This body answers kubectl, not the frontend.
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "database did not answer",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
