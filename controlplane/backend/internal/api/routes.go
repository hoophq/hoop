package api

import (
	"github.com/gin-gonic/gin"
)

// Route paths reachable with no credential of any kind.
//
// Held as data so the closed-route test derives its list from
// engine.Routes() minus this set. Sidecar enrollment is not here: a
// bootstrap credential is authenticated differently, not unauthenticated.
// Keys are "METHOD /path" exactly as gin reports them.
var unauthenticatedRoutes = map[string]bool{
	// Probes: an orchestrator holds no credential, and a 401 reads as down.
	"GET /healthz": true,
	"GET /readyz":  true,

	// The endpoints that mint an admin session in the first place.
	"POST /api/auth/login":    true,
	"POST /api/auth/register": true,
}

// routes registers every route in one place, so "what does this service
// expose, and what guards it" is answered top to bottom. (apierr is its own
// package so feature packages can import it without an import cycle here.)
//
// Two prefixes for two audiences with different credentials:
//
//	/api   an admin with a session, from the browser
//	/v1    a sidecar with a machine credential, from customer infrastructure
//
// Splitting on the prefix makes the guard visible from the path alone and
// lets the test assert one rule per prefix; enrolment is the sole exception
// (bootstrap credential).
//
// Under /api, desired and actual state are subresources of the sidecar:
// config is what should run, status is what does run. The Kubernetes
// spec/status split: the two routinely disagree, and the disagreement is
// the information a merged document could not carry.
func (s *Server) routes(engine *gin.Engine) {
	d := s.deps
	health := newHealth(d.Readiness, d.Version)

	// Probes sit outside both prefixes, auth, and the request timeout.
	engine.GET("/healthz", health.Live)
	engine.GET("/readyz", health.Ready)

	// ---------------------------------------------------------------- /api

	api := engine.Group("/api")

	api.POST("/auth/login", d.AdminAuth.Login)
	api.POST("/auth/register", d.AdminAuth.Register)

	// Everything below requires an admin session.
	//
	// RequireAdmin currently answers 501, so these routes are closed rather
	// than open until admin auth lands; test handlers directly until then. A
	// pass-through stub would ship and leave the config store open.
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

	// Versioned because sidecar fleets do not upgrade in lockstep; /api
	// carries no version because the frontend ships with the backend.
	v1 := engine.Group("/v1")

	// Enrollment presents the bootstrap credential, which RequireSidecar
	// does not accept, so it gets its own guard.
	bootstrapping := v1.Group("")
	bootstrapping.Use(d.RequireBootstrap)
	bootstrapping.POST("/enroll", d.SidecarAuth.Enroll)

	// Everything below presents the rotating credential enrollment issued.
	sidecar := v1.Group("")
	sidecar.Use(d.RequireSidecar)

	sidecar.POST("/credentials/rotate", d.SidecarAuth.Rotate)

	// The polling pair: the sidecar asks what it should run and reports what
	// it does run; nothing is pushed. See "The sidecar contract" in
	// controlplane/backend/CLAUDE.md.
	sidecar.GET("/desiredstate", d.DesiredState.Serve)
	sidecar.POST("/status", d.Inventory.Report)

	// No NoRoute handler: no UI, so no SPA fallback; Gin's 404 is correct
	// and a catch-all would turn a typo into an empty 200.
}
