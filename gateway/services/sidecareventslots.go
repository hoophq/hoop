package services

import (
	"context"
	"sync/atomic"
	"time"
)

// The event ingest and the session hooks it starts share the replica's
// database pool with the handshake, login and the web app. Each gets a fifth
// of the pool, so a backlog of events cannot take every connection.
const defaultSidecarEventSlots = 10

type sidecarEventLanes struct{ ingest, hooks chan struct{} }

// A slot goes back to the lanes it came from, so a resize never loses one.
var sidecarSlots atomic.Pointer[sidecarEventLanes]

func init() { sidecarSlots.Store(newSidecarEventLanes(defaultSidecarEventSlots)) }

func newSidecarEventLanes(n int) *sidecarEventLanes {
	return &sidecarEventLanes{ingest: make(chan struct{}, n), hooks: make(chan struct{}, n)}
}

// SizeSidecarEventSlots sizes the ingest and the hooks to a pool of poolSize
// connections.
func SizeSidecarEventSlots(poolSize int) {
	sidecarSlots.Store(newSidecarEventLanes(max(1, poolSize/5)))
}

// AdmitSidecarEvents takes an ingest slot, waiting at most wait. Not ok means
// the replica is behind on events.
func AdmitSidecarEvents(ctx context.Context, wait time.Duration) (release func(), ok bool) {
	slots := sidecarSlots.Load().ingest
	release = func() { <-slots }
	select {
	case slots <- struct{}{}:
		return release, true
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return release, true
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil, false
}
