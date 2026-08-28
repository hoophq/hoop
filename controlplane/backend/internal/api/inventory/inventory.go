// Package inventory serves what is ACTUALLY running across the fleet.
//
// Scaffold only. Every handler answers 501. EVL-232 owns the implementation.
//
// desiredstate holds what should be true and this holds what is true. The two
// disagreeing is the normal condition, not an error, and showing the
// difference honestly is the product.
//
// Four constraints an implementer will otherwise rediscover the hard way.
// controlplane/backend/CLAUDE.md carries the reasoning.
//
//  1. Storage is memory, deliberately. After a restart the view is empty
//     until sidecars check in again, roughly one poll interval. Say that on
//     screen, and note it rules out running two replicas.
//  2. Liveness is the check-in, never a poller. Nothing in this control plane
//     dials a sidecar, so a sidecar that stops calling is all the signal
//     there is.
//  3. Never report a generation you were not told. It comes from the status
//     the sidecar posted, nowhere else.
//  4. Refusal reasons belong in the response, not only in a log.
package inventory

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

// State is what the fleet view reports for one sidecar.
//
// Defined in the scaffold rather than left to EVL-232 because the frontend
// renders these strings and the status handler writes them, so they are a
// contract between two workstreams before they are an implementation detail of
// either. They are not sidecar vocabulary: the sidecar never sends them, the
// control plane derives them from the last status it received.
//
// Every one of them is derived from two facts and nothing else: how long ago
// the sidecar last checked in, and what it said it was running. There is no
// state meaning "unreachable", because we never reach for it and a word that
// implies we tried would send an operator to check the network.
type State string

const (
	// StateCurrent means the sidecar checked in within the window and is
	// running the generation it was issued.
	StateCurrent State = "current"
	// StateLagging means the sidecar checked in within the window but is
	// running an older generation than the one it was issued. Normal for up
	// to one poll interval after a write; a concern after that.
	StateLagging State = "lagging"
	// StateRejected means the sidecar checked in and reported that it refused
	// the config it was given. It is still running the previous one. Carry
	// the reason: this is the highest-value field in the view and the easiest
	// to drop into a log line and forget.
	StateRejected State = "rejected"
	// StateSilent means no check-in arrived within the window. The record is
	// retained with a last-seen timestamp, so a sidecar restarting does not
	// read as a deletion.
	StateSilent State = "silent"
)

// Handler will hold the in-memory registry once EVL-232 gives it one.
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

// Status returns one sidecar's reported state in full.
//
// Separate from Get for the same reason Kubernetes separates a status
// subresource: the object is what an admin manages and the status is what the
// workload reports, they change at different rates and from different
// directions, and a single document merging them has nowhere to put "asked for
// generation 7, running 6, refused 7 because a certificate path does not
// exist".
func (h *Handler) Status(c *gin.Context) {
	apierr.NotImplemented(c, "reading a sidecar's reported status")
}

// Forget removes a sidecar's record.
//
// Only the record. It does not stop the sidecar, which this control plane
// cannot do, and it does not revoke the credential, which is
// DELETE /api/sidecars/:name/credentials and belongs to EVL-234. A forgotten
// sidecar that still holds a valid credential reappears at its next check-in,
// so EVL-232 has to decide whether Forget implies revoke and say so on screen
// either way.
func (h *Handler) Forget(c *gin.Context) {
	apierr.NotImplemented(c, "forgetting a sidecar")
}

// Report records the status a sidecar posted about itself.
//
// The other half of the polling contract, called on the sidecar's own
// schedule, roughly every 30 seconds with jitter. Jitter matters at fleet
// scale: sidecars started by the same rollout otherwise align and arrive as
// one spike per interval.
//
// The sidecar name comes from the credential, never from the body. A sidecar
// that can name itself in the payload can file a report about any other one.
func (h *Handler) Report(c *gin.Context) {
	apierr.NotImplemented(c, "recording a sidecar status report")
}
