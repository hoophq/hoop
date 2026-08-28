// Package adminauth is signup and signin for the humans who administer the
// control plane.
//
// Scaffold only. Every handler answers 501. EVL-233 owns the implementation.
//
// Four constraints an implementer will otherwise rediscover the hard way.
// controlplane/backend/CLAUDE.md carries the reasoning.
//
//  1. Administration only. The end user does not authenticate with us at
//     all. A requirement beginning "when the end user logs in" is in the
//     wrong product.
//  2. Two populations, never one mechanism. Admin sessions and sidecar
//     credentials are different token types. Sidecar credentials live in
//     package sidecarauth and stay there.
//  3. First-run admin creation must stop working the instant the user table
//     is not empty, and "check then insert" is two statements and therefore a
//     race. It needs a constraint the database enforces.
//  4. Sessions do not live in memory even though inventory does, or every
//     restart signs every admin out mid-task.
//
// One admin role. Auditor and read-only were a gateway concept and should be
// re-derived from what this product needs. gateway/idp is worth reading for
// provider resolution and cached verifiers, and is wired to gateway models,
// gateway request context and gateway role middleware, none of which exist
// here. Read it, do not copy it.
package adminauth

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// ctxKey is the type of every value this package puts on a gin.Context.
//
// A private named type rather than a bare string. gin.Context is one
// namespace shared by every middleware in the process, so an untyped
// c.Set("user", ...) is a collision waiting for a second package that also
// thinks "user" is a reasonable key. The gateway does this with c.Get("context")
// and reads it back with an unchecked type assertion.
type ctxKey string

// ctxKeyAdmin holds the authenticated admin. Read it with CurrentAdmin, never
// with c.Get directly, so exactly one type assertion exists in the codebase.
const ctxKeyAdmin ctxKey = "controlplane.admin"

// Admin is the authenticated administrator. EVL-233 fills it in.
type Admin struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
}

// CurrentAdmin returns the admin RequireAdmin put on the context.
//
// The second return is false when the request did not pass RequireAdmin. A
// handler that ignores it and dereferences anyway is the bug this signature
// exists to make visible.
func CurrentAdmin(c *gin.Context) (Admin, bool) {
	v, ok := c.Get(string(ctxKeyAdmin))
	if !ok {
		return Admin{}, false
	}
	admin, ok := v.(Admin)
	return admin, ok
}

// setCurrentAdmin is the only writer of ctxKeyAdmin. EVL-233 calls it from
// RequireAdmin.
//
//nolint:unused // the seam is declared with the reader; RequireAdmin is a stub.
func setCurrentAdmin(c *gin.Context, admin Admin) {
	c.Set(string(ctxKeyAdmin), admin)
}

// Handler will hold the store and the session issuer once EVL-233 gives it
// them.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// Login exchanges credentials for a session.
func (h *Handler) Login(c *gin.Context) {
	apierr.NotImplemented(c, "admin signin")
}

// Logout revokes the current session.
func (h *Handler) Logout(c *gin.Context) {
	apierr.NotImplemented(c, "admin signout")
}

// Register creates the first admin on an empty deployment.
func (h *Handler) Register(c *gin.Context) {
	apierr.NotImplemented(c, "first-admin registration")
}

// Me returns the signed-in admin.
func (h *Handler) Me(c *gin.Context) {
	apierr.NotImplemented(c, "reading the current admin")
}

// RequireAdmin will reject unauthenticated requests once EVL-233 lands.
//
// It is deliberately a hard 501 rather than a pass-through. A middleware stub
// that calls c.Next() so the routes underneath can be developed is a stub
// that ships: it compiles, every test passes, and the protected routes are
// simply open. Failing closed means the day someone mounts a real handler
// behind this, they cannot avoid noticing it is unguarded.
func (h *Handler) RequireAdmin(c *gin.Context) {
	apierr.NotImplemented(c, "admin authentication")
}
