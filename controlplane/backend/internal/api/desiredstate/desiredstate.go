// Package desiredstate serves what SHOULD be running on each sidecar: admins
// read/write configs under /api, sidecars fetch their own under /v1. Scaffold
// only: every handler answers 501. The document is the sidecar config from
// sidecar/daemon/config.go; the generation is the ETag, never a key inside the
// document; always full documents, never deltas.
package desiredstate

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// Handler will hold the store once the implementation lands.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// Get returns one sidecar's config, for an admin.
func (h *Handler) Get(c *gin.Context) {
	apierr.NotImplemented(c, "reading a sidecar config")
}

// Set writes one sidecar's config and bumps its generation. Generation is
// monotonic per sidecar: bump on every write, never reuse a number. There is
// deliberately no delete: a sidecar never falls back to its bootstrap file,
// so the only exits are replacing the config or removing the sidecar.
func (h *Handler) Set(c *gin.Context) {
	apierr.NotImplemented(c, "writing a sidecar config")
}

// Serve answers a sidecar asking for its own config: 304 when the
// If-None-Match generation is still current, else 200 with the full document
// and an ETag. The sidecar name comes from the credential, never the request,
// or any authenticated sidecar could fetch any other's config.
func (h *Handler) Serve(c *gin.Context) {
	apierr.NotImplemented(c, "serving a sidecar its desired state")
}
