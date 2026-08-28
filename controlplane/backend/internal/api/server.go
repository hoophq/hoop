// Package api wires the control plane's HTTP surface: engine, middleware,
// routes and health, with the feature packages as subpackages.
//
// Engine is separate from Run so tests exercise the production middleware
// chain, not handlers on a bare gin.New().
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

// Timeouts live on the http.Server, not only on handlers. Without
// ReadHeaderTimeout an idle client holds a goroutine and fd forever. All four
// are set because every request here is short (sidecars poll). A future
// streaming endpoint must clear its own deadline via
// http.NewResponseController rather than unset these.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// Longer than handlerTimeout so a slow-but-succeeding response is not
	// truncated mid-write.
	writeTimeout = 60 * time.Second
	idleTimeout  = 120 * time.Second

	// handlerTimeout bounds one /api or /v1 request; probes carry their own
	// shorter bound.
	handlerTimeout = 30 * time.Second
)

// Readiness is what /readyz asks before reporting ready. Declared here so
// gorm stays out of this package; database.Pinger satisfies it and a fake
// drives the 503 branch in tests.
type Readiness interface {
	Ping(ctx context.Context) error
}

// Deps is everything the HTTP surface needs, constructed by the caller.
// Handlers are built in main so routing only decides what is mounted where.
// Guards arrive as gin.HandlerFunc so tests can substitute them.
type Deps struct {
	// Process-wide values.
	Config  config.Config
	Logger  *slog.Logger
	Version string

	// Readiness backs /readyz.
	Readiness Readiness

	// RequireAdmin guards /api; RequireSidecar and RequireBootstrap guard
	// /v1. Three distinct credential populations. See routes.
	RequireAdmin     gin.HandlerFunc
	RequireSidecar   gin.HandlerFunc
	RequireBootstrap gin.HandlerFunc

	// Component handlers.
	AdminAuth    *adminauth.Handler
	DesiredState *desiredstate.Handler
	Inventory    *inventory.Handler
	SidecarAuth  *sidecarauth.Handler
}

// validate refuses a Deps with a hole in it, naming the field; each nil is
// otherwise a request-time dereference deep in the middleware chain.
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

// Server owns the HTTP surface and its dependencies. No package-level state,
// so tests build isolated Servers.
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
	// gin.New, not gin.Default: Default's logger/recovery write unstructured
	// text alongside our structured output.
	engine := gin.New()

	// Trust no proxy: otherwise gin believes any X-Forwarded-For, letting a
	// client spoof its source address.
	if err := engine.SetTrustedProxies(nil); err != nil {
		s.deps.Logger.Warn("failed clearing trusted proxies", "error", err)
	}

	// Order matters: recovery outermost, logging before CORS so rejected
	// requests still appear in the log.
	engine.Use(recovery(s.deps.Logger))
	engine.Use(requestLogger(s.deps.Logger))
	engine.Use(securityHeaders())
	engine.Use(cors(s.deps.Config.CORSAllowedOrigins))

	s.routes(engine)
	return engine
}

// Run serves until ctx is cancelled, then drains in-flight requests within
// the shutdown grace.
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
	// Fresh context: ctx is already cancelled and would make Shutdown return
	// immediately.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.deps.Config.ShutdownGrace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errCh
}
