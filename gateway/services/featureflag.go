package services

import (
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

// WarmFeatureFlagCache loads all org feature flags from the DB
// into the in-process featureflag cache. Called once at gateway startup.
func WarmFeatureFlagCache() {
	orgs, err := models.ListAllOrganizations()
	if err != nil {
		log.Warnf("featureflag: failed listing orgs for cache warm: %v", err)
		return
	}
	for _, org := range orgs {
		flags, err := models.ListOrgFeatureFlags(org.ID)
		if err != nil {
			log.Warnf("featureflag: failed listing flags for org %s: %v", org.ID, err)
			continue
		}
		snapshot := make(map[string]bool, len(flags))
		for _, f := range flags {
			snapshot[f.Name] = f.Enabled
		}
		featureflag.SetAll(org.ID, snapshot)
	}
	log.Infof("featureflag: warmed cache for %d orgs", len(orgs))
}
