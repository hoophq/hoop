// Package sidecarmirrorstatus sets offline the connections that mirror a
// sidecar listener when their sidecar stops checking in. The handshake sets
// them online (gateway/api/sidecar); nothing else sees a sidecar stop.
package sidecarmirrorstatus

import (
	"context"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

const (
	// sweepInterval adds to models.SidecarMirrorOfflineAfter: a mirror reads
	// offline at most this long after its sidecar crosses the limit.
	sweepInterval = 30 * time.Second

	// sweepTimeout keeps at most one sweep in flight.
	sweepTimeout = 20 * time.Second
)

// Run sweeps once, then every sweepInterval until ctx is done. Every replica
// runs it; the update is idempotent.
func Run(ctx context.Context, db *gorm.DB) {
	sweep(ctx, db)

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, db)
		}
	}
}

func sweep(ctx context.Context, db *gorm.DB) {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	n, err := models.MarkStaleSidecarConnectionsOffline(ctx, db)
	if err != nil {
		log.Errorf("sidecar mirrors: %v", err)
		return
	}
	if n > 0 {
		log.Infof("sidecar mirrors: set %v connection(s) offline, no sidecar check-in", n)
	}
}
