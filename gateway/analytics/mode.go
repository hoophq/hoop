package analytics

import (
	"sync"

	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

// Per-org analytics_mode cache. Reads happen on every Identify/Track call,
// so we keep the value in memory and only hit the DB at startup and when an
// admin changes the mode via PUT /orgs/analytics-mode.

var (
	modeCacheMu sync.RWMutex
	modeCache   = map[string]string{}
)

// GetMode returns the cached analytics_mode for an org. Falls back to
// anonymous when the org isn't in the cache so we never accidentally emit
// PII for an org we don't know about.
func GetMode(orgID string) string {
	if orgID == "" {
		return models.AnalyticsModeAnonymous
	}
	modeCacheMu.RLock()
	mode, ok := modeCache[orgID]
	modeCacheMu.RUnlock()
	if !ok || !models.IsValidAnalyticsMode(mode) {
		return models.AnalyticsModeAnonymous
	}
	return mode
}

// SetMode updates the cache for a single org. Call this from any code path
// that changes the persisted analytics_mode (signup, admin PUT).
func SetMode(orgID, mode string) {
	if orgID == "" || !models.IsValidAnalyticsMode(mode) {
		return
	}
	modeCacheMu.Lock()
	modeCache[orgID] = mode
	modeCacheMu.Unlock()
}

// ModeAtBoot is the mode the default organization gets at every boot.
// ANALYTICS_TRACKING decides when set; otherwise the license does: an
// enterprise organization is opted out, a free one reports identified.
func ModeAtBoot(tracking string, licenseData []byte, hostname string) string {
	switch tracking {
	case "enabled":
		return models.AnalyticsModeIdentified
	case "disabled":
		return models.AnalyticsModeDisabled
	}

	l, err := license.Parse(licenseData, hostname)
	if err == nil && l.Payload.Type == license.EnterpriseType {
		return models.AnalyticsModeDisabled
	}

	return models.AnalyticsModeIdentified
}

// WarmModeCache hydrates the cache from the orgs table. Called once at
// gateway startup so the first Identify/Track does not pay a DB round-trip.
func WarmModeCache() {
	orgs, err := models.ListAllOrganizations()
	if err != nil {
		log.Warnf("analytics: failed listing orgs for mode cache warm: %v", err)
		return
	}
	modeCacheMu.Lock()
	defer modeCacheMu.Unlock()
	modeCache = make(map[string]string, len(orgs))
	for _, o := range orgs {
		if models.IsValidAnalyticsMode(o.AnalyticsMode) {
			modeCache[o.ID] = o.AnalyticsMode
		}
	}
	log.Infof("analytics: warmed mode cache for %d orgs", len(orgs))
}
