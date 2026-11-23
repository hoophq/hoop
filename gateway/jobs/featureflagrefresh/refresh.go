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
	baseInterval = 10 * time.Second
	// maxJitter spreads the replicas' reads apart.
	maxJitter = 5 * time.Second

	// MaxInterval bounds the time between two reads: a flag change reaches
	// every replica, and the agents connected to it, within MaxInterval plus
	// one read.
	MaxInterval = baseInterval + maxJitter

	// refreshTimeout keeps a slow read from delaying the next one long.
	refreshTimeout = 5 * time.Second
)

// Run refreshes the flags every baseInterval plus up to maxJitter until ctx
// is done. The boot already loaded them (services.WarmFeatureFlagCache).
func Run(ctx context.Context, db *gorm.DB) {
	run(ctx, db, featureflag.Default(), nextInterval, transport.SendFeatureFlagUpdateToOrg)
}

// run refreshes cache after each interval(), and calls notify for each org
// whose flags changed. notify must not block: a slow agent must not delay the
// next read (transport.FeatureFlagDelivery does not).
func run(ctx context.Context, db *gorm.DB, cache *featureflag.Cache, interval func() time.Duration, notify func(orgID string)) {
	timer := time.NewTimer(interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		refresh(ctx, db, cache, notify)
		timer.Reset(interval())
	}
}

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
