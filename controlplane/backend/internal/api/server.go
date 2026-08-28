// Package api wires the control plane's HTTP surface: engine, middleware,
// routes and health, with the feature packages as subpackages.
//
// Engine is built separately from Run so a test exercises the same middleware
// chain production does. A test that mounts handlers on a bare gin.New()
// proves the handlers work and proves nothing about the stack in front of
// them, which is where auth and CORS live.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/adminauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/desiredstate"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/inventory"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/sidecarauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

// Timeouts on the http.Server, not only on handlers.
//
// Without ReadHeaderTimeout a client that opens a connection and sends
// nothing holds a goroutine and a file descriptor until the process dies,
// which is a denial of service costing the caller one connection. The gateway
// has no timeouts on its API server; tunnel/ipc/server.go does.
//
// All four are set because every request this service serves has a defined
// length. Sidecars poll: a config fetch and a status post, both short. An
// earlier version left ReadTimeout and WriteTimeout unset to protect a
// long-lived WebSocket per sidecar, since a hijacked connection inherits the
// deadlines net/http already set on it. There is no socket to protect now, and
// leaving those two unset without that reason is just a slow-client hole.
//
// If a later endpoint does stream, long-poll or SSE for approvals is the
// candidate, it must clear its own deadline with http.NewResponseController
// rather than unset these. Per-handler is the right scope for a per-handler
// need; server-wide is how one streaming route removes the bound from every
// other one.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// Longer than handlerTimeout on purpose. A write deadline shorter than
	// the handler's own deadline truncates the response of a request that was
	// about to succeed, and the client sees a broken connection rather than a
	// timeout it can report.
	writeTimeout = 60 * time.Second
	idleTimeout  = 120 * time.Second

	// handlerTimeout bounds one /api or /v1 request. The probes are outside
	// it: they carry their own shorter bound.
	handlerTimeout = 30 * time.Second
)

// Readiness is what /readyz asks before reporting the process ready.
//
// One method, declared here rather than imported, because the consumer defines
// the interface it needs. database.Pinger satisfies it. Holding this instead
// of a *gorm.DB keeps gorm out of this package entirely and lets a test drive
// the 503 branch with a fake that fails on demand.
type Readiness interface {
	Ping(ctx context.Context) error
}

// Deps is everything the HTTP surface needs, constructed by the caller.
//
// The component handlers are built in main and handed in, rather than
// constructed inside routes. Routing then decides only what is mounted where
// and what guards it, which is the one question this package should answer.
// When EVL-231 gives desiredstate a store, that store is wired in main and no
// signature here changes.
//
// The two middlewares arrive as gin.HandlerFunc rather than being pulled off
// the handlers, because a route table's guards are the thing worth being able
// to substitute in a test.
type Deps struct {
	// Config, Logger and Version are the process-wide values.
	Config  config.Config
	Logger  *slog.Logger
	Version string

	// Readiness backs /readyz.
	Readiness Readiness

	// RequireAdmin guards /api. RequireSidecar and RequireBootstrap guard
	// /v1. Three, not one, because admins, enrolled sidecars and sidecars
	// still holding a bootstrap credential are three populations presenting
	// three different kinds of credential. See routes.
	RequireAdmin     gin.HandlerFunc
	RequireSidecar   gin.HandlerFunc
	RequireBootstrap gin.HandlerFunc

	// The component handlers.
	AdminAuth    *adminauth.Handler
	DesiredState *desiredstate.Handler
	Inventory    *inventory.Handler
	SidecarAuth  *sidecarauth.Handler
}

// validate refuses a Deps with a hole in it.
//
// Loudly, at construction, naming the field. Every one of these is a nil
// dereference somewhere in the middleware chain otherwise, on whichever
// request happens to arrive first, with a stack that names gin rather than the
// wiring that was wrong.
func (d Deps) validate() error {
	missing := []string{}
	for name, ok := range map[string]bool{
		"Logger":           d.Logger != nil,
		"Readiness":        d.Readiness != nil,
		"RequireAdmin":     d.RequireAdmin != nil,
		"RequireSidecar":   d.RequireSidecar != nil,
		"RequireBootstrap": d.RequireBootstrap != nil,
		"AdminAuth":        d.AdminAuth != nil,
		"DesiredState":     d.DesiredState != nil,
		"Inventory":        d.Inventory != nil,
		"SidecarAuth":      d.SidecarAuth != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing) // map order, so sort for a stable message
		return fmt.Errorf("api.Deps is missing %v", missing)
	}
	return nil
}

// Server owns the HTTP surface and its dependencies.
//
// Everything it needs arrives through New. There is no package-level config,
// no package-level logger and no package-level database handle, so a test
// builds a Server with exactly the dependencies the case needs and two tests
// cannot interfere.
type Server struct {
	deps Deps
}

// New returns a Server, or an error naming what the caller left out.
func New(deps Deps) (*Server, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &Server{deps: deps}, nil
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
		s.deps.Logger.Warn("failed clearing trusted proxies", "error", err)
	}

	// Order matters. Recovery is outermost so it catches panics from every
	// later middleware. Logging wraps the rest so a request rejected by CORS
	// still appears in the log.
	engine.Use(recovery(s.deps.Logger))
	engine.Use(requestLogger(s.deps.Logger))
	engine.Use(securityHeaders())
	engine.Use(cors(s.deps.Config.CORSAllowedOrigins))

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
		Addr:              s.deps.Config.ListenAddr,
		Handler:           s.Engine(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		s.deps.Logger.Info("control plane api listening", "addr", s.deps.Config.ListenAddr)
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

	s.deps.Logger.Info("shutting down, draining in-flight requests", "grace", s.deps.Config.ShutdownGrace.String())
	// A fresh context: ctx is already cancelled, so passing it would make
	// Shutdown return immediately and drain nothing.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.deps.Config.ShutdownGrace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errCh
}
