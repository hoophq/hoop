package services

import (
	"math"
	"sync"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// The plane tells each sidecar how often to handshake, on the answer's
// HeartbeatSecondsHeader. The interval grows with the organization's fleet so
// the plane sees about the same handshake rate whatever the fleet's size: a
// thousand sidecars at the default is ~17 handshakes/s, and that is the rate
// held. The cost is how fast a config edit reaches a running sidecar and how
// stale last-seen reads; the mirror sweeper and the session reaper count
// their thresholds in multiples of the served interval, so neither reads a
// sidecar as gone for calling at the rate it was told.
const (
	// sidecarsPerHeartbeatStep is the fleet size each step of the interval
	// covers: up to this many sidecars handshake at the default, twice that
	// at twice the default, and so on.
	sidecarsPerHeartbeatStep = 1000
	// sidecarHeartbeatMaxSeconds caps the interval. The sidecar refuses
	// anything past an hour; ten minutes keeps an edit within reach.
	sidecarHeartbeatMaxSeconds = 600
	// sidecarFleetSizeTTL is how long a replica trusts its count of an
	// organization's sidecars. The count moves the interval by whole steps
	// of a thousand, so a stale count costs nothing a fleet would notice.
	sidecarFleetSizeTTL = 5 * time.Minute
)

type fleetSize struct {
	n  int64
	at time.Time
}

var (
	fleetSizesMu sync.Mutex
	fleetSizes   = map[string]fleetSize{} // orgID -> count
)

// SidecarHeartbeatSeconds is the interval the plane tells the organization's
// sidecars to handshake at. A count that cannot be read answers the default:
// the handshake must not fail over a hint.
func SidecarHeartbeatSeconds(db *gorm.DB, orgID string) int {
	n, err := cachedFleetSize(db, orgID)
	if err != nil {
		return models.SidecarHeartbeatDefaultSeconds
	}
	return heartbeatSecondsFor(n)
}

// heartbeatSecondsFor is the interval for a fleet of n sidecars.
func heartbeatSecondsFor(n int64) int {
	steps := int(math.Ceil(float64(n) / sidecarsPerHeartbeatStep))
	if steps < 1 {
		steps = 1
	}
	return min(steps*models.SidecarHeartbeatDefaultSeconds, sidecarHeartbeatMaxSeconds)
}

func cachedFleetSize(db *gorm.DB, orgID string) (int64, error) {
	fleetSizesMu.Lock()
	cached, ok := fleetSizes[orgID]
	fleetSizesMu.Unlock()
	if ok && time.Since(cached.at) < sidecarFleetSizeTTL {
		return cached.n, nil
	}
	n, err := models.CountSidecars(db, orgID)
	if err != nil {
		return 0, err
	}
	fleetSizesMu.Lock()
	fleetSizes[orgID] = fleetSize{n: n, at: time.Now()}
	fleetSizesMu.Unlock()
	return n, nil
}
