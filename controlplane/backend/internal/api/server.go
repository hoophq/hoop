// Package api serves the control plane's HTTP API.
//
// Scaffold. It answers a health check and nothing else. Routes, authentication
// and everything else are TBD; see controlplane/backend/CLAUDE.md.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

// Server timeouts.
//
// All four are set. Without ReadHeaderTimeout a client that opens a connection
// and sends nothing holds a goroutine and a file descriptor until the process
// dies, which is a denial of service costing the caller one connection.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
)

// Server is the HTTP API.
type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	engine  *gin.Engine
	version string
}

// New builds the server and registers its routes.
func New(cfg config.Config, logger *slog.Logger, version string) *Server {
	// gin.New, not gin.Default: Default installs gin's own unstructured logger
	// and recovery alongside ours.
	engine := gin.New()

	// Trust no proxy. Gin's default trusts every X-Forwarded-For, so the first
	// call to c.ClientIP() would return whatever the client claimed.
	if err := engine.SetTrustedProxies(nil); err != nil {
		logger.Warn("failed clearing trusted proxies", "error", err)
	}

	engine.Use(gin.Recovery(), requestLogger(logger))

	s := &Server{cfg: cfg, logger: logger, engine: engine, version: version}
	s.routes()
	return s
}

// routes registers every route, in one place, read top to bottom.
//
// There is no NoRoute handler. This binary serves no UI, so an unmatched path
// is a mistake and must look like one.
func (s *Server) routes() {
	h := newHealth(s.version)

	s.engine.GET("/healthz", h.live)
}

// Handler exposes the router so a test can drive it without a socket.
func (s *Server) Handler() http.Handler { return s.engine }

// Run serves until ctx is cancelled, then drains for ShutdownGrace.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.engine,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errc := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", s.cfg.ListenAddr, "version", s.version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutting down", "grace", s.cfg.ShutdownGrace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// requestLogger logs one line per request.
//
// It logs the matched route, never the raw path, the query string or the body.
// All three routinely carry credentials in this product, and a log line is the
// easiest place in a system to retain one forever.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			"method", c.Request.Method,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration", time.Since(start),
		)
	}
}
