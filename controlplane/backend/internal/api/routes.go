package api

import (
	"github.com/gin-gonic/gin"
)

// Route paths reachable with no credential of any kind.
//
// Held as data, not written down in prose, because the test that asserts every
// other route is closed derives its list from engine.Routes() minus this set.
// A route added outside a guarded group therefore fails the test instead of
// quietly joining the open set.
//
// Four entries, and each one has to justify itself. Sidecar enrollment is not
// here: it authenticates with a bootstrap credential rather than an admin
// session, which makes it authenticated differently, not unauthenticated. That
// distinction is the whole reason RequireBootstrap exists as its own
// middleware instead of the enroll route being waved through.
//
// Keys are "METHOD /path" exactly as gin reports them.
var unauthenticatedRoutes = map[string]bool{
	// Probes. An orchestrator cannot hold a credential, and a readiness probe
	// that returns 401 reads to it as a service that is down.
	"GET /healthz": true,
	"GET /readyz":  true,

	// The two endpoints that exist so an admin can obtain a session in the
	// first place.
	"POST /api/auth/login":    true,
	"POST /api/auth/register": true,
}

// routes registers every route in one place.
//
// One function, read top to bottom, is how you answer "what does this service
// expose, and what guards it" without grepping. Per-feature registration
// scatters that answer across four packages and makes an accidentally
// unauthenticated route invisible at review. This is the reason apierr is its
// own package: the feature packages import apierr, this package imports the
// feature packages, and no package under api imports this one.
//
// Two prefixes, because there are two audiences and they hold different
// credentials.
//
//	/api   an admin with a session, from the browser
//	/v1    a sidecar with a machine credential, from customer infrastructure
//
// Splitting on the prefix rather than per component means the guard is visible
// from the path alone, in a log line and in a proxy rule, without knowing
// which ticket owns the handler. It is also what lets the test assert one rule
// per prefix. Enrolment is the one exception, because it presents a bootstrap
// credential rather than a long-lived one, and the test names it as such.
//
// Under /api the resource is the sidecar, and desired and actual state are
// subresources of it:
//
//	/api/sidecars/:name/config   what should run   EVL-231
//	/api/sidecars/:name/status   what does run     EVL-232
//
// That is the Kubernetes spec and status split, and the reason is the same one
// this whole product rests on: the two routinely disagree, and the disagreement
// is the information. One document merging them has nowhere to put "asked for
// generation 7, running 6, refused 7 because the certificate path does not
// exist".
//
// Paths are still cheap to change. controlplane/frontend calls the gateway on
// :8009 for everything and is not a client of this API yet. That stops being
// true the day someone repoints it.
func (s *Server) routes(engine *gin.Engine) {
	d := s.deps
	health := newHealth(d.Readiness, d.Version)

	// Probes sit outside both prefixes, outside auth, and outside the request
	// timeout, because they carry their own shorter one.
	engine.GET("/healthz", health.Live)
	engine.GET("/readyz", health.Ready)

	// ---------------------------------------------------------------- /api

	api := engine.Group("/api")

	api.POST("/auth/login", d.AdminAuth.Login)
	api.POST("/auth/register", d.AdminAuth.Register)

	// Everything below requires an admin session.
	//
	// RequireAdmin currently answers 501, so these routes are closed rather
	// than open while EVL-233 is outstanding. That is deliberate and it does
	// mean a component landing before admin auth cannot be exercised through
	// the router. Test the handler directly until then. The alternative, a
	// middleware that calls c.Next() so development is convenient, is a
	// middleware that ships and leaves the config store unauthenticated.
	admin := api.Group("")
	admin.Use(d.RequireAdmin)

	admin.POST("/auth/logout", d.AdminAuth.Logout)
	admin.GET("/auth/me", d.AdminAuth.Me)

	admin.GET("/sidecars", d.Inventory.List)
	admin.GET("/sidecars/:name", d.Inventory.Get)
	admin.DELETE("/sidecars/:name", d.Inventory.Forget)

	admin.GET("/sidecars/:name/config", d.DesiredState.Get)
	admin.PUT("/sidecars/:name/config", d.DesiredState.Set)

	admin.GET("/sidecars/:name/status", d.Inventory.Status)

	admin.DELETE("/sidecars/:name/credentials", d.SidecarAuth.Revoke)

	// ----------------------------------------------------------------- /v1

	// Versioned because sidecars are, and permanently so. A fleet does not
	// upgrade in lockstep, so this prefix has to keep working for a sidecar
	// built a year ago. /api carries no version: the frontend ships with the
	// backend and there is no old client to keep working.
	v1 := engine.Group("/v1")

	// Enrollment presents the bootstrap credential, which is precisely the
	// credential RequireSidecar does not accept. Its own guard, so neither
	// group is loosened to fit the other. Both stubs are EVL-234's.
	bootstrapping := v1.Group("")
	bootstrapping.Use(d.RequireBootstrap)
	bootstrapping.POST("/enroll", d.SidecarAuth.Enroll)

	// Everything below presents the rotating credential enrollment issued.
	sidecar := v1.Group("")
	sidecar.Use(d.RequireSidecar)

	sidecar.POST("/credentials/rotate", d.SidecarAuth.Rotate)

	// The polling pair. The sidecar asks what it should be running and
	// reports what it is running; nothing is pushed to it. See "The sidecar
	// contract" in controlplane/backend/CLAUDE.md.
	sidecar.GET("/desiredstate", d.DesiredState.Serve)
	sidecar.POST("/status", d.Inventory.Report)

	// No NoRoute handler. An unmatched path gets Gin's 404, which is correct:
	// this binary serves no UI, so there is no SPA fallback and a catch-all
	// would turn a typo in a service file into an empty 200.
}
