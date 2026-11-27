// Package featureflagrefresh reads the feature flags from the database again
// on every replica. PUT /api/feature-flags/:name updates only the replica that
// answers it; without this loop the others served the old value until they
// restarted, so turning a flag off was no kill switch.
package featureflagrefresh

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/transport"
	"gorm.io/gorm"
)

const (
	baseInterval = 7 * time.Second
	// maxJitter spreads the replicas' reads apart.
	maxJitter = 3 * time.Second

	// maxInterval bounds the time from the start of one read to the start of
	// the next.
	maxInterval = baseInterval + maxJitter

	// refreshTimeout bounds one read.
	refreshTimeout = 5 * time.Second

	// propagationBound is how long a flag change saved on another replica
	// takes to reach this replica's cache when the next read succeeds: it
	// starts within maxInterval of the last one and ends within
	// refreshTimeout. A failed read moves the change to the read after it.
	// Delivery to agents is not covered: a blocked agent stream holds it.
	propagationBound = maxInterval + refreshTimeout
)

// Run refreshes the flags every baseInterval plus up to maxJitter until ctx
// is done. The boot already loaded them (services.WarmFeatureFlagCache).
func Run(ctx context.Context, db *gorm.DB) {
	cache := featureflag.Default()
	run(ctx, nextInterval, func(ctx context.Context) {
		refresh(ctx, db, cache, transport.SendFeatureFlagUpdateToOrg)
	})
}

// run calls read interval() after the previous read started, not after it
// ended: a slow read must not add to the wait for the next one.
func run(ctx context.Context, interval func() time.Duration, read func(ctx context.Context)) {
	timer := time.NewTimer(interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		start := time.Now()
		read(ctx)
		timer.Reset(nextReadDelay(interval(), time.Since(start)))
	}
}

// nextReadDelay is how long to wait after a read that took elapsed, so the
// next read starts interval after the last one started.
func nextReadDelay(interval, elapsed time.Duration) time.Duration {
	return max(0, interval-elapsed)
}

// refresh reads the flags into cache, and calls notify for each org whose
// flags changed. notify must not block: a slow agent must not delay the next
// read (transport.FeatureFlagDelivery does not).
func refresh(ctx context.Context, db *gorm.DB, cache *featureflag.Cache, notify func(orgID string)) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	changed, err := services.RefreshFeatureFlagCache(ctx, db, cache)
	if err != nil {
		log.Warnf("featureflag: failed refreshing flags, reason=%v", err)
		return
	}
	for _, orgID := range changed {
		log.With("org", orgID).Infof("featureflag: applied flags changed in the database")
		// The agents of this replica hold their own copy.
		notify(orgID)
	}
}

func nextInterval() time.Duration {
	return baseInterval + rand.N(maxJitter)
}
