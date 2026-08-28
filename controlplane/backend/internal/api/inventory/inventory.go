// Package inventory serves what is ACTUALLY running across the fleet; scaffold
// only, every handler answers 501. Storage is in-memory by design: empty after
// a restart and rules out two replicas. Liveness is the check-in — nothing here
// dials a sidecar. Never report a generation the sidecar did not post.
package inventory

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// State is what the fleet view reports for one sidecar. These strings are a
// contract with the frontend: the sidecar never sends them, the control plane
// derives them from check-in recency and the last reported status.
type State string

const (
	// StateCurrent: checked in within the window, running the issued generation.
	StateCurrent State = "current"
	// StateLagging: running an older generation; normal for up to one poll
	// interval after a write, a concern after that.
	StateLagging State = "lagging"
	// StateRejected: the sidecar refused its issued config and still runs the
	// previous one. Carry the reason into the response.
	StateRejected State = "rejected"
	// StateSilent: no check-in within the window; the record is retained with
	// a last-seen timestamp, so a restart does not read as a deletion.
	StateSilent State = "silent"
)

// Handler will hold the in-memory registry once the implementation lands.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// List returns every known sidecar and its state, for an admin.
func (h *Handler) List(c *gin.Context) {
	apierr.NotImplemented(c, "listing the sidecar fleet")
}

// Get returns one sidecar: its identity and the summary of both states.
func (h *Handler) Get(c *gin.Context) {
	apierr.NotImplemented(c, "reading a sidecar")
}

// Status returns one sidecar's reported state in full. Separate from Get,
// like a Kubernetes status subresource: the object and the status change at
// different rates and from different directions.
func (h *Handler) Status(c *gin.Context) {
	apierr.NotImplemented(c, "reading a sidecar's reported status")
}

// Forget removes a sidecar's record — only the record. It does not stop the
// sidecar or revoke its credential, so one still holding a valid credential
// reappears at its next check-in.
func (h *Handler) Forget(c *gin.Context) {
	apierr.NotImplemented(c, "forgetting a sidecar")
}

// Report records the status a sidecar posted about itself, on the sidecar's
// own jittered ~30s schedule. The name comes from the credential, never the
// body, or a sidecar could file a report about any other one.
func (h *Handler) Report(c *gin.Context) {
	apierr.NotImplemented(c, "recording a sidecar status report")
}
