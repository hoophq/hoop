// Package desiredstate serves what SHOULD be running on each sidecar.
//
// Scaffold only. Every handler answers 501 and names EVL-231, which owns the
// implementation and owns this package's store and types too. Package by
// feature: the handler, the queries and the row types live here together, not
// spread across a handler layer and a models layer.
//
// Three constraints an implementer will otherwise rediscover the hard way.
// controlplane/CLAUDE.md carries the reasoning.
//
//  1. The document is a sidecar config. Do not define a second schema.
//     sidecar/daemon/config.go already defines and validates it.
//  2. Config.Validate is not pure. It reads certificate files from disk
//     through BuildDownstreamTLS, and those paths only resolve on the
//     sidecar. Deciding what the control plane can honestly check at write
//     time is part of EVL-231.
//  3. Generation travels in the wire envelope, never inside the document.
//     The sidecar parses with DisallowUnknownFields, so one extra key fails
//     the whole config. See internal/wire.
package desiredstate

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

const ticket = "EVL-231"

// Handler will hold the store once EVL-231 gives it one.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// List returns every sidecar config.
func (h *Handler) List(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "listing sidecar configs")
}

// Get returns one sidecar's config.
func (h *Handler) Get(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "reading a sidecar config")
}

// Create stores a new sidecar config.
func (h *Handler) Create(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "creating a sidecar config")
}

// Update replaces a sidecar config and bumps its generation.
func (h *Handler) Update(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "updating a sidecar config")
}

// Delete removes a sidecar config.
func (h *Handler) Delete(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "deleting a sidecar config")
}
