// Package httpapi wires the control plane's HTTP surface.
//
// Engine is built separately from Run so a test exercises the same middleware
// chain production does. A test that mounts handlers on a bare gin.New()
// proves the handlers work and proves nothing about the stack in front of
// them, which is where auth and CORS live.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

// Timeouts on the http.Server, not just on handlers.
//
// Without ReadHeaderTimeout a client that opens a connection and sends
// nothing holds a goroutine and a file descriptor until the process dies,
// which is a denial of service costing the caller one socket. The gateway has
// no timeouts on its API server; tunnel/ipc/server.go does.
//
// ReadTimeout and WriteTimeout are deliberately unset, and this is the one
// place in the file where the default is wrong on purpose.
//
// The transport this product is built around is one long-lived WebSocket per
// sidecar. A WebSocket handler hijacks the connection, and net/http does not
// clear the deadlines it already set on that net.Conn. With a 60 second
// WriteTimeout, every sidecar socket dies at 60 seconds with an i/o timeout
// that names nothing, and it reads as a flaky reconnect loop rather than as a
// server setting. Whole-request deadlines belong on the requests that have a
// defined length, so they live on the /api group instead. See requestTimeout
// and apiRequestTimeout.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second

	// apiRequestTimeout bounds one /api request. It is not applied to the
	// probes, which have their own shorter bound, and it must not be applied
	// to the sidecar socket when EVL-234 mounts it.
	apiRequestTimeout = 30 * time.Second
)

// Server owns the HTTP surface and its dependencies.
//
// Everything it needs arrives through New. There is no package-level config,
// no package-level logger and no package-level database handle, so a test
// builds a Server with exactly the dependencies the case needs and two tests
// cannot interfere.
type Server struct {
	cfg     config.Config
	db      *gorm.DB
	logger  *slog.Logger
	version string
}

// New returns a Server. version is the build string, reported by /healthz.
func New(cfg config.Config, db *gorm.DB, logger *slog.Logger, version string) *Server {
	return &Server{cfg: cfg, db: db, logger: logger, version: version}
}

// Engine constructs the Gin engine with the full middleware chain.
func (s *Server) Engine() *gin.Engine {
	// gin.New, not gin.Default. Default installs Gin's own logger and
	// recovery, which write unstructured text to stdout and would sit
	// alongside this service's structured output rather than replacing it.
	engine := gin.New()

	// Trust no proxy by default. Gin otherwise believes X-Forwarded-For from
	// anyone, so a client can set its own apparent source address, and
	// anything built on top of that address (rate limiting, audit, an allow
	// list) is decided by the attacker.
	if err := engine.SetTrustedProxies(nil); err != nil {
		s.logger.Warn("failed clearing trusted proxies", "error", err)
	}

	// Order matters. Recovery is outermost so it catches panics from every
	// later middleware. Logging wraps the rest so a request rejected by CORS
	// still appears in the log.
	engine.Use(recovery(s.logger))
	engine.Use(requestLogger(s.logger))
	engine.Use(securityHeaders())
	engine.Use(cors(s.cfg.CORSAllowedOrigins))

	s.routes(engine)
	return engine
}

// Run serves until ctx is cancelled, then drains.
//
// Drain rather than drop. A control plane restarting mid-request while an
// admin saves a config should finish writing it, and the shutdown grace is
// the bound on how long that is allowed to take.
func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Engine(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("control plane api listening", "addr", s.cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutting down, draining in-flight requests", "grace", s.cfg.ShutdownGrace.String())
	// A fresh context: ctx is already cancelled, so passing it would make
	// Shutdown return immediately and drain nothing.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errCh
}
