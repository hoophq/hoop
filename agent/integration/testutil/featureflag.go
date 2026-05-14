//go:build integration

package testutil

import (
	"encoding/json"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
)

// SetAgentFeatureFlags pushes a FeatureFlagUpdate packet at the agent so
// subsequent IsEnabled checks for the listed flags return true. The agent
// processes the packet on the recv loop; a brief sleep gives the loop
// time to apply the snapshot before the test proceeds.
//
// Pass an empty map to clear all flags (the agent treats Update with an
// empty snapshot as "reset everything to disabled").
func SetAgentFeatureFlags(t T, tr *MockTransport, flags map[string]bool) {
	t.Helper()
	raw, err := json.Marshal(flags)
	if err != nil {
		t.Fatalf("featureflag: failed marshaling flags: %v", err)
	}
	pkt := &pb.Packet{
		Type: pbgateway.FeatureFlagUpdate,
		Spec: map[string][]byte{
			pb.SpecFeatureFlagsKey: raw,
		},
	}
	tr.Inject(pkt)

	// The recv loop is async; give it a moment to land. 50ms is well past
	// any realistic delay between Inject() returning and the agent
	// observing the packet, but short enough to keep tests snappy.
	time.Sleep(50 * time.Millisecond)
}
