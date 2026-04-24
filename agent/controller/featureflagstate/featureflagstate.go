package featureflagstate

import (
	"encoding/json"
	"sync"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
)

var (
	mu    sync.RWMutex
	flags = map[string]bool{}
)

// Update replaces the entire flag state from a FeatureFlagUpdate packet spec.
func Update(spec map[string][]byte) {
	raw, ok := spec[pb.SpecFeatureFlagsKey]
	if !ok || len(raw) == 0 {
		return
	}
	var snapshot map[string]bool
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		log.Warnf("featureflagstate: failed to unmarshal flags: %v", err)
		return
	}
	mu.Lock()
	flags = snapshot
	mu.Unlock()
	log.Infof("featureflagstate: updated %d flags", len(snapshot))
}

// IsEnabled returns whether the named flag is enabled.
// Returns false for unknown flags.
func IsEnabled(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return flags[name]
}

// Snapshot returns a copy of the current flag state.
func Snapshot() map[string]bool {
	mu.RLock()
	defer mu.RUnlock()
	cp := make(map[string]bool, len(flags))
	for k, v := range flags {
		cp[k] = v
	}
	return cp
}
