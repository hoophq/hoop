package api

import (
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// requestLogger emits one structured line per request.
//
// The logger arrives as a parameter rather than through a package-level
// default, so a test captures output by handing in its own handler.
//
// Written here rather than pulled from gin-contrib because the alternatives
// depend on zap or logrus, and the logger for this module is stdlib slog. It
// is twenty lines.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// Status and size are only meaningful after the handler chain has
		// run, which is why this reads them below c.Next rather than above.
		path := c.Request.URL.Path
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		// Never log the query string or the body. Both routinely carry
		// credentials in this product, and a log line is the easiest place in
		// a system to accidentally retain one forever.
		if err := c.Errors.Last(); err != nil {
			attrs = append(attrs, "error", err.Err)
		}

		switch status := c.Writer.Status(); {
		case status >= http.StatusInternalServerError:
			logger.Error("request failed", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("request rejected", attrs...)
		case isProbe(path):
			// A successful probe at a five second interval is seventeen
			// thousand identical lines a day. A failing one is not: it lands
			// in the 5xx branch above and is loud.
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
		// The client gets nothing but the status. A panic value routinely
		// contains a pointer address or a fragment of a query.
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
	})
}

// securityHeaders sets the headers a browser needs to not do something
// helpful on our behalf.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// This is a JSON API with no UI of its own, so the strictest policy is
		// also the correct one. It changes if the binary ever serves the React
		// app, which it deliberately does not today.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}

// cors answers cross-origin requests for the configured origins only.
//
// This is the one place the CORS rule is argued, and the other mentions of it
// point here. The gateway sends Access-Control-Allow-Origin: * together with
// Access-Control-Allow-Credentials: true. Browsers reject that pairing, and
// it would be unsafe if they honoured it: any page on the internet could read
// authenticated responses. This is an admin control plane, so the default is
// an empty allow list and a development machine opts in with
// CONTROLPLANE_CORS_ALLOWED_ORIGINS=http://localhost:5173.
//
// No Access-Control-Expose-Headers. EVL-233 has not chosen between a cookie,
// a body field and a header for the admin session, and a header named here
// before that decision is a decision made by omission.
func cors(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		// An echoed origin means the response varies by it, and a cache that
		// does not know that will serve one tenant's response to another.
		c.Writer.Header().Add("Vary", "Origin")

		if origin != "" && slices.Contains(allowedOrigins, origin) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}

		// Preflight is answered whether or not the origin was allowed. A
		// disallowed one simply carries no Allow-Origin header, and the
		// browser blocks the real request. Returning an error status instead
		// produces a console message that sends people debugging the server.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
