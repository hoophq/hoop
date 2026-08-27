// Package inventory serves what is ACTUALLY running across the fleet.
//
// Scaffold only. Every handler answers 501 and names EVL-232, which owns the
// implementation.
//
// desiredstate holds what should be true and this holds what is true. The two
// disagreeing is the normal condition, not an error, and showing the
// difference honestly is the product.
//
// Four constraints an implementer will otherwise rediscover the hard way.
// controlplane/CLAUDE.md carries the reasoning.
//
//  1. Storage is memory, deliberately. After a restart the view is empty
//     until sidecars reconnect. Say that on screen, and note it rules out
//     running two replicas.
//  2. Liveness is the socket plus the heartbeat, never a poller. Nothing in
//     this control plane dials a sidecar.
//  3. Never report a generation you were not told. It comes from config.ack
//     and from hello, nowhere else.
//  4. NACK reasons belong in the response, not only in a log.
package inventory

import (
	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
)

const ticket = "EVL-232"

// State is what the fleet view reports for one sidecar.
//
// Defined in the scaffold rather than left to EVL-232 because the frontend
// renders these strings and the socket handler writes them, so they are a
// contract between two workstreams before they are an implementation detail
// of either. They are not wire vocabulary: the sidecar never sends them, the
// control plane derives them.
type State string

const (
	// StateConnected means the socket is open, the heartbeat is current, and
	// the acked generation equals the issued one.
	StateConnected State = "connected"
	// StateStale means the socket is open but the acked generation is behind
	// the issued one by more than the heartbeat window.
	StateStale State = "stale"
	// StateRejected means the sidecar sent config.nack. Carry the reason.
	StateRejected State = "rejected"
	// StateDisconnected means the socket closed. The record is retained with
	// a last-seen timestamp, so a restart does not read as a deletion.
	StateDisconnected State = "disconnected"
)

// Handler will hold the in-memory registry once EVL-232 gives it one.
type Handler struct{}

// New returns a Handler.
func New() *Handler { return &Handler{} }

// List returns every known sidecar and its state.
func (h *Handler) List(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "listing the sidecar fleet")
}

// Get returns one sidecar's live state.
func (h *Handler) Get(c *gin.Context) {
	apierr.NotImplemented(c, ticket, "reading a sidecar's live state")
}
