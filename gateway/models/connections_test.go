package models

import (
	"testing"
)

func TestHasEmptyRules(t *testing.T) {
	tests := []struct {
		name   string
		input  ConnectionGuardRailRules
		expect bool
	}{
		{
			name: "Both rules empty arrays",
			input: ConnectionGuardRailRules{
				GuardRailInputRules:  []byte("[]"),
				GuardRailOutputRules: []byte("[]"),
			},
			expect: true,
		},
		{
			name: "Non-empty input rules",
			input: ConnectionGuardRailRules{
				GuardRailInputRules:  []byte("[{\"key\":\"value\"}]"),
				GuardRailOutputRules: []byte("[]"),
			},
			expect: false,
		},
		{
			name: "Non-empty output rules",
			input: ConnectionGuardRailRules{
				GuardRailInputRules:  []byte("[]"),
				GuardRailOutputRules: []byte("[{\"key\":\"value\"}]"),
			},
			expect: false,
		},
		{
			name: "Both rules non-empty",
			input: ConnectionGuardRailRules{
				GuardRailInputRules:  []byte("[{\"key\":\"value1\"}]"),
				GuardRailOutputRules: []byte("[{\"key\":\"value2\"}]"),
			},
			expect: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.input.HasEmptyRules()
			if result != test.expect {
				t.Errorf("HasEmptyRules() returned %v, expected %v.", result, test.expect)
			}
		})
	}
}
