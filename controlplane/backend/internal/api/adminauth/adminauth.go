// Package adminauth is signup and signin for control-plane administrators.
// Scaffold only: every handler answers 501. Admins only — end users never
// authenticate here, and sidecar credentials stay in sidecarauth. First-admin
// creation needs a database-enforced constraint; sessions must survive restarts.
package adminauth

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// ctxKey types every value this package puts on a gin.Context, so keys cannot
// collide with other middleware using bare string keys.
type ctxKey string

// ctxKeyAdmin holds the authenticated admin. Read it with CurrentAdmin, never
// with c.Get directly, so exactly one type assertion exists in the codebase.
const ctxKeyAdmin ctxKey = "controlplane.admin"

// Admin is the authenticated administrator.
type Admin struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
}

// CurrentAdmin returns the admin RequireAdmin put on the context; false when
// the request did not pass RequireAdmin.
func CurrentAdmin(c *gin.Context) (Admin, bool) {
	v, ok := c.Get(string(ctxKeyAdmin))
	if !ok {
		return Admin{}, false
	}
	admin, ok := v.(Admin)
	return admin, ok
}

// setCurrentAdmin is the only writer of ctxKeyAdmin; RequireAdmin calls it.
//
//nolint:unused // seam declared with the reader; RequireAdmin is a stub.
func setCurrentAdmin(c *gin.Context, admin Admin) {
	c.Set(string(ctxKeyAdmin), admin)
}

// Handler will hold the store and session issuer once the implementation lands.
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

// RequireAdmin will reject unauthenticated requests once implemented.
// Deliberately a hard 501, never a c.Next() pass-through: a stub that passes
// through ships routes that are simply open. Failing closed keeps that visible.
func (h *Handler) RequireAdmin(c *gin.Context) {
	apierr.NotImplemented(c, "admin authentication")
}
