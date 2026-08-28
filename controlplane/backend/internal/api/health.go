package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// health answers the liveness probe.
type health struct {
	version string
}

func newHealth(version string) *health { return &health{version: version} }

// live reports that the process is up and serving.
//
// Never put a dependency check here. A dependency blip would then restart
// every replica at once, turning a recoverable failure into an outage.
// Readiness belongs on its own endpoint, and there is nothing to be ready for
// yet, so it is TBD.
func (h *health) live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": h.version,
	})
}
