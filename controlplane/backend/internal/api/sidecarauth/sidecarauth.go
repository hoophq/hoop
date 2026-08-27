// Package sidecarauth is how a sidecar proves who it is, and how it knows the
// control plane is genuine.
//
// Scaffold only. Every handler answers 501 and names EVL-234, which owns the
// implementation and whose first deliverable is a written decision, not code.
//
// Four constraints an implementer will otherwise rediscover the hard way.
// controlplane/CLAUDE.md carries the reasoning.
//
//  1. Discovery and trust are two questions. Discovery, what address do I
//     dial, is an environment variable. Trust is the hard one.
//  2. Trust cannot be bootstrapped from nothing. SPIRE ships
//     insecure_bootstrap for testing only and says plainly that without an
//     anchor a machine-in-the-middle controls the infrastructure. Any design
//     claiming to need no pre-shared anything is trusting the network.
//  3. It is mutual. A sidecar that accepts policy from whatever answers on
//     that address is a sidecar an attacker disarms by answering first.
//  4. JWT with rotation is the right shape AFTER bootstrap and does not solve
//     bootstrap. Do not let the two get conflated.
//
// hello is where this hooks into the wire: the credential is presented at
// connect, a failed check produces hello.reject carrying a reason, and no
// config flows before hello.ok. See internal/wire.
package sidecarauth

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

const ticket = "EVL-234"

// Anchor is the trust root that turns a bootstrap credential into a sidecar
// identity.
//
// Declared in the scaffold, unimplemented, because it is the seam EVL-234 is
// required to build behind. Naming it here means the development token and
// the eventual real anchor are the same interface from the first commit,
// rather than the development token becoming a shape that has to be
// refactored out under time pressure later.
//
// EVL-234 ships a named development anchor before the real one: a single
// static token, behind this interface, that announces what it is. A config
// key saying dev_token, a warn line at startup, and a refusal to run when
// config.Config.IsProduction is true. Not a silent default that quietly
// becomes the shipping behaviour, which is how a placeholder turns into a
// CVE. IsProduction exists for that refusal and for nothing else.
type Anchor interface {
	// Verify returns the sidecar name the credential is good for.
	//
	// The name rather than a boolean, deliberately: a sidecar asserts its
	// name in the hello payload, and a verifier that only says yes or no lets
	// any authenticated sidecar claim any other's config.
	//
	// The context is not decoration. Every anchor worth shipping, Kubernetes
	// TokenReview, AWS instance identity, GCE identity tokens, is a network
	// call to the platform's verification API, and one with no deadline is
	// one that hangs the connect path when the platform is slow.
	Verify(ctx context.Context, credential string) (sidecarName string, err error)

	// Name identifies the anchor in logs and in the fleet view, so an
	// operator can tell at a glance that a deployment is still running the
	// development token.
	Name() string
}

// Handler will hold the Anchor and the credential store once EVL-234 gives it
// them.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// Enroll exchanges a bootstrap credential for a rotating one.
func (h *Handler) Enroll(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "sidecar enrollment")
}

// Rotate issues a fresh credential before the current one expires.
func (h *Handler) Rotate(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "sidecar credential rotation")
}

// Revoke withdraws a sidecar's credential.
//
// Listed as a route in the scaffold because revocation is the part of a
// credential design that is usually forgotten and the first thing a security
// review asks about. An empty route here is harder to forget than an absent
// one.
func (h *Handler) Revoke(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "sidecar credential revocation")
}
