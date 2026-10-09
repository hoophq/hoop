// Package featureflagstate holds the feature flags one gateway stream sent.
package featureflagstate

import (
	"encoding/json"
	"sync"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
)

// State is the flag snapshot of one gateway stream. The zero value has every
// flag off, so a stream that sends no snapshot fails closed (DEP-289).
type State struct {
	mu    sync.RWMutex
	flags map[string]bool
}

// Update replaces the entire flag state from a FeatureFlagUpdate packet spec.
// A spec without the flags key leaves the state unchanged.
func (s *State) Update(spec map[string][]byte) {
	raw, ok := spec[pb.SpecFeatureFlagsKey]
	if !ok || len(raw) == 0 {
		return
	}
	var snapshot map[string]bool
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		log.Warnf("featureflagstate: failed to unmarshal flags: %v", err)
		return
	}
	s.mu.Lock()
	s.flags = snapshot
	s.mu.Unlock()
	log.Infof("featureflagstate: updated %d flags", len(snapshot))
}

// IsEnabled returns whether the named flag is enabled.
// Returns false for unknown flags.
func (s *State) IsEnabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.flags[name]
}
