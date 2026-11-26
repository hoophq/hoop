package services

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// featureFlagRefreshAttempts bounds the reads one refresh makes when a flag
// write on this replica lands while it reads.
const featureFlagRefreshAttempts = 3

// WarmFeatureFlagCache loads all org feature flags from the DB
// into the in-process featureflag cache. Called once at gateway startup.
func WarmFeatureFlagCache() {
	if _, err := RefreshFeatureFlagCache(context.Background(), models.DB, featureflag.Default()); err != nil {
		log.Warnf("featureflag: failed warming cache: %v", err)
		return
	}
	log.Infof("featureflag: warmed cache")
}

// RefreshFeatureFlagCache makes cache hold the flags stored in db, and
// returns the orgs whose flags changed. PUT /api/feature-flags/:name updates
// the cache of the replica that answers it; the others read the change here.
func RefreshFeatureFlagCache(ctx context.Context, db *gorm.DB, cache *featureflag.Cache) ([]string, error) {
	for range featureFlagRefreshAttempts {
		gen := cache.Generation()
		rows, err := models.ListAllOrgFeatureFlags(db.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("listing feature flags: %w", err)
		}
		snapshot := map[string]map[string]bool{}
		for _, f := range rows {
			if snapshot[f.OrgID] == nil {
				snapshot[f.OrgID] = map[string]bool{}
			}
			snapshot[f.OrgID][f.Name] = f.Enabled
		}
		if changed, ok := cache.Replace(gen, snapshot); ok {
			return changed, nil
		}
	}
	return nil, fmt.Errorf("a flag write on this replica raced each of %d reads", featureFlagRefreshAttempts)
}
