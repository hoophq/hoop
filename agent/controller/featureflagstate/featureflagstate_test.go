package featureflagstate

import (
	"encoding/json"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// reset clears the package-level flag state so each test starts from a
// known baseline. The package keeps its state in a process-global map by
// design (matching the gateway's per-process cache), so test isolation
// has to happen at the test layer.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	flags = map[string]bool{}
	mu.Unlock()
}

func TestUpdateAndIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]bool
		query    string
		expected bool
	}{
		{
			name:     "enabled flag returns true",
			flags:    map[string]bool{"experimental.example": true},
			query:    "experimental.example",
			expected: true,
		},
		{
			name:     "disabled flag returns false",
			flags:    map[string]bool{"experimental.example": false},
			query:    "experimental.example",
			expected: false,
		},
		{
			name:     "unknown flag returns false",
			flags:    map[string]bool{"experimental.other": true},
			query:    "experimental.example",
			expected: false,
		},
		{
			name:     "empty state returns false",
			flags:    map[string]bool{},
			query:    "experimental.example",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			raw, err := json.Marshal(tc.flags)
			if err != nil {
				t.Fatalf("failed to marshal flags: %v", err)
			}
			Update(map[string][]byte{pb.SpecFeatureFlagsKey: raw})

			got := IsEnabled(tc.query)
			if got != tc.expected {
				t.Errorf("IsEnabled(%q) = %v, want %v", tc.query, got, tc.expected)
			}
		})
	}
}

func TestUpdateReplacesEntireState(t *testing.T) {
	reset(t)
	first, _ := json.Marshal(map[string]bool{"flag_a": true, "flag_b": true})
	Update(map[string][]byte{pb.SpecFeatureFlagsKey: first})

	second, _ := json.Marshal(map[string]bool{"flag_a": true})
	Update(map[string][]byte{pb.SpecFeatureFlagsKey: second})

	if IsEnabled("flag_b") {
		t.Error("flag_b should have been removed by the second Update")
	}
	if !IsEnabled("flag_a") {
		t.Error("flag_a should still be enabled after the second Update")
	}
}

func TestUpdateIgnoresMissingKey(t *testing.T) {
	reset(t)
	initial, _ := json.Marshal(map[string]bool{"sticky_flag": true})
	Update(map[string][]byte{pb.SpecFeatureFlagsKey: initial})

	// An Update call with no flags key should leave existing state intact —
	// the gateway sometimes sends partial packets and we don't want to
	// silently wipe the agent's flag snapshot.
	Update(map[string][]byte{})

	if !IsEnabled("sticky_flag") {
		t.Error("sticky_flag should not have been cleared by an Update without the flags key")
	}
}

func TestSnapshotReturnsCopy(t *testing.T) {
	reset(t)
	raw, _ := json.Marshal(map[string]bool{"copy_test": true})
	Update(map[string][]byte{pb.SpecFeatureFlagsKey: raw})

	snap := Snapshot()
	snap["copy_test"] = false
	snap["added_in_caller"] = true

	if !IsEnabled("copy_test") {
		t.Error("Snapshot must return an independent copy; mutation leaked back into the package state")
	}
	if IsEnabled("added_in_caller") {
		t.Error("Snapshot must return an independent copy; new key leaked back into the package state")
	}
}
