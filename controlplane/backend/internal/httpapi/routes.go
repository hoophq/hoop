package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/adminauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/desiredstate"
	"github.com/hoophq/hoop/controlplane/backend/internal/inventory"
	"github.com/hoophq/hoop/controlplane/backend/internal/sidecarauth"
)

// Route paths that are reachable without an admin session.
//
// Held as data, not written down in prose, because the test that asserts
// every other route is closed derives its list from engine.Routes() minus
// this set. A route added outside the protected group therefore fails the
// test instead of quietly joining the open set.
//
// Keys are "METHOD /path" exactly as gin reports them.
var unauthenticatedRoutes = map[string]bool{
	// Probes. An orchestrator cannot hold a credential, and a readiness probe
	// that returns 401 reads to it as a service that is down.
	"GET /healthz": true,
	"GET /readyz":  true,

	// The endpoints that exist so an admin can obtain a session.
	"POST /api/login":    true,
	"POST /api/register": true,

	// Sidecar enrollment authenticates with a bootstrap credential rather
	// than an admin session, so it cannot sit behind RequireAdmin. EVL-234
	// owns its own verification. This group is not unauthenticated, it is
	// authenticated differently, and the distinction must survive review.
	"POST /api/sidecar-auth/enroll": true,
	"POST /api/sidecar-auth/rotate": true,
}

// routes registers every route in one place.
//
// One function, read top to bottom, is how you answer "what does this service
// expose, and what guards it" without grepping. Per-feature registration
// scatters that answer across four packages and makes an accidentally
// unauthenticated route invisible at review. This is the reason apierr is its
// own package: the feature packages import apierr, httpapi imports the
// feature packages, and nothing imports httpapi.
//
// Paths are provisional. They are named per component so each of the four
// workstreams owns a prefix and no two collide, but nothing outside this
// repository depends on them yet: controlplane/frontend still calls the
// gateway on :8009 for everything. Renaming them is cheap right now and stops
// being cheap the moment the frontend is repointed.
func (s *Server) routes(engine *gin.Engine) {
	health := newHealth(s.db, s.version)
	admin := adminauth.New()
	configs := desiredstate.New()
	fleet := inventory.New()
	sidecars := sidecarauth.New()

	// Probes sit outside /api, outside auth, and outside the /api request
	// timeout, because they carry their own shorter one.
	engine.GET("/healthz", health.Live)
	engine.GET("/readyz", health.Ready)

	// /api matches the base path controlplane/frontend's axios client already
	// assumes, so repointing it later is a change of host, not of every
	// service file.
	//
	// The timeout is mounted on this group and not on the engine. When
	// EVL-234 adds the sidecar WebSocket it must go outside this group, or
	// every sidecar drops at 30 seconds.
	api := engine.Group("/api")
	api.Use(requestTimeout(apiRequestTimeout))

	api.POST("/login", admin.Login)
	api.POST("/register", admin.Register)

	sidecarAuth := api.Group("/sidecar-auth")
	sidecarAuth.POST("/enroll", sidecars.Enroll)
	sidecarAuth.POST("/rotate", sidecars.Rotate)

	// Everything below requires an admin session.
	//
	// RequireAdmin currently answers 501, so these routes are closed rather
	// than open while EVL-233 is outstanding. That is deliberate and it does
	// mean a component landing before admin auth cannot be exercised through
	// the router. Test the handler directly until then. The alternative, a
	// middleware that calls c.Next() so development is convenient, is a
	// middleware that ships and leaves the config store unauthenticated.
	protected := api.Group("")
	protected.Use(admin.RequireAdmin)

	protected.POST("/logout", admin.Logout)
	protected.GET("/userinfo", admin.UserInfo)

	// Desired state: what SHOULD run. Owned by EVL-231.
	protected.GET("/sidecar-configs", configs.List)
	protected.POST("/sidecar-configs", configs.Create)
	protected.GET("/sidecar-configs/:name", configs.Get)
	protected.PUT("/sidecar-configs/:name", configs.Update)
	protected.DELETE("/sidecar-configs/:name", configs.Delete)

	// Inventory: what IS running. Owned by EVL-232. A separate prefix from
	// sidecar-configs on purpose, because the whole product claim is that the
	// two are different things and routinely disagree. One resource serving
	// both would invite a handler that merges them and hides it.
	protected.GET("/fleet", fleet.List)
	protected.GET("/fleet/:name", fleet.Get)

	protected.DELETE("/sidecar-auth/credentials/:name", sidecars.Revoke)

	// No NoRoute handler. An unmatched path gets Gin's 404, which is correct:
	// this binary serves no UI, so there is no SPA fallback and a catch-all
	// would turn a typo in a service file into an empty 200.
}
