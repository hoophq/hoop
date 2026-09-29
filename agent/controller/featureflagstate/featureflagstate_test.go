package featureflagstate

import (
	"encoding/json"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

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
			var s State
			raw, err := json.Marshal(tc.flags)
			if err != nil {
				t.Fatalf("failed to marshal flags: %v", err)
			}
			s.Update(map[string][]byte{pb.SpecFeatureFlagsKey: raw})

			got := s.IsEnabled(tc.query)
			if got != tc.expected {
				t.Errorf("IsEnabled(%q) = %v, want %v", tc.query, got, tc.expected)
			}
		})
	}
}

func TestUpdateReplacesEntireState(t *testing.T) {
	var s State
	first, _ := json.Marshal(map[string]bool{"flag_a": true, "flag_b": true})
	s.Update(map[string][]byte{pb.SpecFeatureFlagsKey: first})

	second, _ := json.Marshal(map[string]bool{"flag_a": true})
	s.Update(map[string][]byte{pb.SpecFeatureFlagsKey: second})

	if s.IsEnabled("flag_b") {
		t.Error("flag_b should have been removed by the second Update")
	}
	if !s.IsEnabled("flag_a") {
		t.Error("flag_a should still be enabled after the second Update")
	}
}

func TestUpdateIgnoresMissingKey(t *testing.T) {
	var s State
	initial, _ := json.Marshal(map[string]bool{"sticky_flag": true})
	s.Update(map[string][]byte{pb.SpecFeatureFlagsKey: initial})

	// An Update call with no flags key should leave existing state intact —
	// the gateway sometimes sends partial packets and we don't want to
	// silently wipe the agent's flag snapshot.
	s.Update(map[string][]byte{})

	if !s.IsEnabled("sticky_flag") {
		t.Error("sticky_flag should not have been cleared by an Update without the flags key")
	}
}
