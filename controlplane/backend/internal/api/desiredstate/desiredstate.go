// Package desiredstate serves what SHOULD be running on each sidecar.
//
// Scaffold only. Every handler answers 501. EVL-231 owns the implementation,
// and owns this package's store and types too. Package by feature: the
// handler, the queries and the row types live here together, not spread
// across a handler layer and a models layer.
//
// It serves two audiences from one store. An admin reads and writes a config
// under /api; a sidecar fetches its own under /v1 and is never written to.
// Nothing here dials a sidecar.
//
// Four constraints an implementer will otherwise rediscover the hard way.
// controlplane/backend/CLAUDE.md carries the reasoning.
//
//  1. The document is a sidecar config. Do not define a second schema.
//     sidecar/daemon/config.go already defines and validates it.
//  2. Config.Validate is not pure. It reads certificate files from disk
//     through BuildDownstreamTLS, and those paths only resolve on the
//     sidecar. Deciding what the control plane can honestly check at write
//     time is part of EVL-231.
//  3. The generation is the ETag, never a key inside the document. The
//     sidecar parses with DisallowUnknownFields, so one extra key fails the
//     whole config, and a config that fails to parse is a sidecar running the
//     previous one until somebody notices.
//  4. Full documents, never deltas. A delta needs both ends to agree on the
//     base, and after a restart on either side they do not.
package desiredstate

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// Handler will hold the store once EVL-231 gives it one.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// Get returns one sidecar's config, for an admin.
func (h *Handler) Get(c *gin.Context) {
	apierr.NotImplemented(c, "reading a sidecar config")
}

// Set writes one sidecar's config and bumps its generation.
//
// One route rather than a create and an update, because the MVP is one config
// per named sidecar and the name is in the path, so PUT is the whole
// operation. Generation is monotonic per sidecar: bump on every write, never
// reuse a number.
//
// There is deliberately no delete. Removing a config would leave a sidecar
// enforcing rules the control plane no longer knows about, and the only honest
// ways out of that are replacing the config or removing the sidecar, which is
// DELETE /api/sidecars/:name. Non-negotiable 3 is why: a sidecar never falls
// back to its bootstrap file.
func (h *Handler) Set(c *gin.Context) {
	apierr.NotImplemented(c, "writing a sidecar config")
}

// Serve answers a sidecar asking for its own config.
//
// The polling half of the contract. The sidecar sends If-None-Match carrying
// the generation it is running; this returns 304 when that is still current
// and 200 with the whole document plus an ETag when it is not. 304 is the
// steady state and costs a header exchange, which is what makes a 30 second
// interval across a large fleet affordable.
//
// The sidecar name comes from the credential, never from the request. A
// handler that reads a name out of a query parameter lets any authenticated
// sidecar fetch any other's config, which is why sidecarauth.Anchor.Verify
// returns a name rather than a boolean.
func (h *Handler) Serve(c *gin.Context) {
	apierr.NotImplemented(c, "serving a sidecar its desired state")
}
