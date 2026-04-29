package featureflag

import (
	"sort"
	"sync"

	"github.com/hoophq/hoop/common/log"
)

type Stability string

const (
	StabilityExperimental Stability = "experimental"
	StabilityBeta         Stability = "beta"
)

type Component string

const (
	ComponentGateway Component = "gateway"
	ComponentAgent   Component = "agent"
	ComponentClient  Component = "client"
)

type Flag struct {
	Name        string
	Description string
	Default     bool
	Stability   Stability
	Components  []Component
}

// catalog is the single source of truth for all known feature flags.
// A flag not registered here cannot be enabled, stored, or read.
var catalog = map[string]Flag{
	"experimental.nightly_flag": {
		Name:        "experimental.nightly_flag",
		Description: "Example flag for testing the feature flag system (no-op)",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway, ComponentAgent, ComponentClient},
	},
	"experimental.log_exec_input": {
		Name:        "experimental.log_exec_input",
		Description: "Include the truncated exec input as a structured log attribute on the agent (for SIEM export). May log sensitive content.",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentAgent},
	},
}

// All returns every registered flag, sorted by name.
func All() []Flag {
	flags := make([]Flag, 0, len(catalog))
	for _, f := range catalog {
		flags = append(flags, f)
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// Lookup returns the flag definition if it exists in the catalog.
func Lookup(name string) (Flag, bool) {
	f, ok := catalog[name]
	return f, ok
}

// --- per-org cache used by gateway (process-local) ---

var (
	cacheMu sync.RWMutex
	// orgID -> flagName -> enabled
	cache = map[string]map[string]bool{}
)

// Set updates the in-process cache for a single flag in one org.
func Set(orgID, name string, enabled bool) {
	if _, ok := catalog[name]; !ok {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache[orgID] == nil {
		cache[orgID] = map[string]bool{}
	}
	cache[orgID][name] = enabled
}

// SetAll replaces the entire flag snapshot for an org.
func SetAll(orgID string, flags map[string]bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	filtered := make(map[string]bool, len(flags))
	for name, enabled := range flags {
		if _, ok := catalog[name]; ok {
			filtered[name] = enabled
		}
	}
	cache[orgID] = filtered
}

// IsEnabled returns the effective value for a flag in an org.
// Unknown flags return false and log a warning.
func IsEnabled(orgID, name string) bool {
	f, ok := catalog[name]
	if !ok {
		log.Warnf("featureflag: unknown flag %q, returning false", name)
		return false
	}
	cacheMu.RLock()
	orgFlags, orgOK := cache[orgID]
	if orgOK {
		if val, found := orgFlags[name]; found {
			cacheMu.RUnlock()
			return val
		}
	}
	cacheMu.RUnlock()
	return f.Default
}

// SnapshotForOrg returns the effective boolean map for every catalog flag
// in the given org. Used to populate /serverinfo and gRPC packets.
func SnapshotForOrg(orgID string) map[string]bool {
	cacheMu.RLock()
	orgFlags := cache[orgID]
	cacheMu.RUnlock()

	snapshot := make(map[string]bool, len(catalog))
	for name, f := range catalog {
		if orgFlags != nil {
			if val, found := orgFlags[name]; found {
				snapshot[name] = val
				continue
			}
		}
		snapshot[name] = f.Default
	}
	return snapshot
}
