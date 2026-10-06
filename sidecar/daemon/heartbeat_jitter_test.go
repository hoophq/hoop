package daemon

import (
	"testing"
	"time"
)

// Each heartbeat wait stays inside every ± heartbeatJitter, and the waits
// differ, so a fleet started together does not stay in step.
func TestTheHeartbeatWaitIsJittered(t *testing.T) {
	low := time.Duration(float64(heartbeatEvery) * (1 - heartbeatJitter))
	high := time.Duration(float64(heartbeatEvery) * (1 + heartbeatJitter))
	distinct := map[time.Duration]bool{}
	for range 200 {
		d := jittered(heartbeatEvery)
		if d < low || d >= high {
			t.Fatalf("wait %s is outside [%s, %s)", d, low, high)
		}
		distinct[d] = true
	}
	if len(distinct) < 2 {
		t.Error("every wait was the same")
	}
}
