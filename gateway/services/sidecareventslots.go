package services

import (
	"context"
	"time"
)

// The event ingest and the session hooks it starts share the replica's
// database pool with the handshake, login and the web app. Each gets a fifth
// of the pool, so a backlog of events cannot take every connection.
const defaultSidecarEventSlots = 10

var (
	sidecarEventSlots = make(chan struct{}, defaultSidecarEventSlots)
	sidecarHookSlots  = make(chan struct{}, defaultSidecarEventSlots)
)

// SizeSidecarEventSlots sizes the ingest and the hooks to a pool of poolSize
// connections. Call it at boot, before the API and the reaper start.
func SizeSidecarEventSlots(poolSize int) {
	n := max(1, poolSize/5)
	sidecarEventSlots = make(chan struct{}, n)
	sidecarHookSlots = make(chan struct{}, n)
}

// AdmitSidecarEvents takes an ingest slot, waiting at most wait. Not ok means
// the replica is behind on events.
func AdmitSidecarEvents(ctx context.Context, wait time.Duration) (release func(), ok bool) {
	slots := sidecarEventSlots
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
