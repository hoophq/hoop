// Package sidecarauth is how a sidecar proves who it is, and how it knows the
// control plane is genuine. Scaffold only: every handler answers 501. Trust is
// mutual and cannot be bootstrapped from nothing — some pre-shared anchor is
// required. Sidecars poll, so every /v1 request is verified on its own.
package sidecarauth

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// Anchor is the trust root that turns a bootstrap credential into a sidecar
// identity. The development anchor is a named static token behind this same
// interface: a dev_token config key, a warning at startup, and a refusal to
// run when config.Config.IsProduction is true — never a silent default.
type Anchor interface {
	// Verify returns the sidecar name the credential is good for — a name,
	// not a boolean, so handlers never take identity from the path or body.
	// Real anchors are network calls; honor the context deadline.
	Verify(ctx context.Context, credential string) (sidecarName string, err error)

	// Name identifies the anchor in logs and the fleet view, so an operator
	// can spot a deployment still on the development token.
	Name() string
}

// Handler will hold the Anchor and credential store once the implementation lands.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// Enroll exchanges a bootstrap credential for a rotating one.
func (h *Handler) Enroll(c *gin.Context) {
	apierr.NotImplemented(c, "sidecar enrollment")
}

// Rotate issues a fresh credential before the current one expires.
func (h *Handler) Rotate(c *gin.Context) {
	apierr.NotImplemented(c, "sidecar credential rotation")
}

// Revoke withdraws a sidecar's credential. A route in the scaffold because
// revocation is the part of a credential design most often forgotten.
func (h *Handler) Revoke(c *gin.Context) {
	apierr.NotImplemented(c, "sidecar credential revocation")
}

// RequireBootstrap guards enrollment. Separate from RequireSidecar: the two
// accept different credentials, and a bootstrap credential RequireSidecar
// accepted would never have to be exchanged. Hard 501, never a pass-through,
// so the stub cannot ship an open enrollment endpoint.
func (h *Handler) RequireBootstrap(c *gin.Context) {
	apierr.NotImplemented(c, "bootstrap credential verification")
}

// RequireSidecar guards every other /v1 route: verifies the rotating
// credential, resolves the sidecar name via Anchor.Verify, and puts the name
// on the context. Hard 501 until implemented — the surface stays closed.
func (h *Handler) RequireSidecar(c *gin.Context) {
	apierr.NotImplemented(c, "sidecar authentication")
}
