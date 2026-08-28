package api

import (
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// requestLogger emits one structured line per request. Hand-rolled because
// gin-contrib's loggers depend on zap or logrus and this module uses slog.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// Status and size are only meaningful after c.Next.
		path := c.Request.URL.Path
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		// Never log the query string or body: both routinely carry
		// credentials here.
		if err := c.Errors.Last(); err != nil {
			attrs = append(attrs, "error", err.Err)
		}

		switch status := c.Writer.Status(); {
		case status >= http.StatusInternalServerError:
			logger.Error("request failed", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("request rejected", attrs...)
		case isProbe(path):
			// Successful probes would flood the log; a failing one lands in
			// the 5xx branch and stays loud.
			logger.Debug("request", attrs...)
		default:
			logger.Info("request", attrs...)
		}
	}
}

func isProbe(path string) bool { return path == "/healthz" || path == "/readyz" }

// recovery turns a panic into a 500 and keeps the process alive.
func recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.Error("panic recovered while serving request",
			"panic", recovered,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
		)
		// Status only: a panic value can carry pointer addresses or query
		// fragments.
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
	})
}

// securityHeaders sets the browser hardening headers.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// JSON API with no UI, so the strictest CSP is correct; revisit only
		// if this binary ever serves the React app.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}

// cors answers cross-origin requests for the configured origins only.
//
// The one place the CORS rule is argued. Never pair Allow-Origin: * with
// Allow-Credentials: true (the gateway does; browsers reject it, and
// honouring it would let any page read authenticated responses). Default is
// an empty allow list; dev opts in via
// CONTROLPLANE_CORS_ALLOWED_ORIGINS=http://localhost:5173.
//
// No Access-Control-Expose-Headers: the admin session transport is not yet
// chosen, and naming a header now would decide it by omission.
func cors(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		// Echoed origin varies the response; caches must know via Vary.
		c.Writer.Header().Add("Vary", "Origin")

		if origin != "" && slices.Contains(allowedOrigins, origin) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}

		// Preflight is answered for every origin; a disallowed one just
		// carries no Allow-Origin header, and the browser blocks the real
		// request. An error status only sends people debugging the server.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
