package models

import (
	"testing"
	"time"
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

func TestComputeSecretsUpdatedAt(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	envsA := map[string]string{"envvar:HOST": "aGVsbG8=", "envvar:PORT": "NTQzMg=="}
	envsB := map[string]string{"envvar:HOST": "d29ybGQ=", "envvar:PORT": "NTQzMg=="}

	tests := []struct {
		name     string
		isInsert bool
		prev     *time.Time
		prevEnvs map[string]string
		nextEnvs map[string]string
		want     *time.Time
	}{
		{
			name:     "insert with envs stamps now",
			isInsert: true,
			nextEnvs: envsA,
			want:     &now,
		},
		{
			name:     "insert with no envs returns nil",
			isInsert: true,
			nextEnvs: map[string]string{},
			want:     nil,
		},
		{
			name:     "insert with nil envs returns nil",
			isInsert: true,
			nextEnvs: nil,
			want:     nil,
		},
		{
			name:     "update with empty envs preserves prev",
			isInsert: false,
			prev:     &earlier,
			prevEnvs: envsA,
			nextEnvs: map[string]string{},
			want:     &earlier,
		},
		{
			name:     "update with unchanged envs preserves prev",
			isInsert: false,
			prev:     &earlier,
			prevEnvs: envsA,
			nextEnvs: envsA,
			want:     &earlier,
		},
		{
			name:     "update with changed envs stamps now",
			isInsert: false,
			prev:     &earlier,
			prevEnvs: envsA,
			nextEnvs: envsB,
			want:     &now,
		},
		{
			name:     "update with added key stamps now",
			isInsert: false,
			prev:     &earlier,
			prevEnvs: map[string]string{"envvar:HOST": "aGVsbG8="},
			nextEnvs: envsA,
			want:     &now,
		},
		{
			name:     "update with removed key stamps now",
			isInsert: false,
			prev:     &earlier,
			prevEnvs: envsA,
			nextEnvs: map[string]string{"envvar:HOST": "aGVsbG8="},
			want:     &now,
		},
		{
			name:     "update from nil prev with changed envs stamps now",
			isInsert: false,
			prev:     nil,
			prevEnvs: map[string]string{"envvar:HOST": "old"},
			nextEnvs: map[string]string{"envvar:HOST": "new"},
			want:     &now,
		},
		{
			name:     "update from nil prev with unchanged envs returns nil",
			isInsert: false,
			prev:     nil,
			prevEnvs: envsA,
			nextEnvs: envsA,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSecretsUpdatedAt(tt.isInsert, tt.prev, tt.prevEnvs, tt.nextEnvs, now)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if got != nil && !got.Equal(*tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvsMapEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, map[string]string{}, true},
		{"same single entry", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"different value", map[string]string{"k": "v"}, map[string]string{"k": "w"}, false},
		{"different key", map[string]string{"a": "v"}, map[string]string{"b": "v"}, false},
		{"different size", map[string]string{"k": "v"}, map[string]string{"k": "v", "k2": "v2"}, false},
		{"key order does not matter",
			map[string]string{"a": "1", "b": "2"},
			map[string]string{"b": "2", "a": "1"},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envsMapEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("envsMapEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestParseTagSelectorQuery(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		want     string
		wantErr  bool
	}{
		{
			name:     "empty selector",
			selector: "",
			want:     "[]",
		},
		{
			name:     "single equality",
			selector: "env=prod",
			want:     `[{"key":"env","op":"=","val":"prod"}]`,
		},
		{
			name:     "single inequality",
			selector: "env!=prod",
			want:     `[{"key":"env","op":"!=","val":"prod"}]`,
		},
		{
			name:     "distinct keys keep their order",
			selector: "env=prod,team!=infra",
			want:     `[{"key":"env","op":"=","val":"prod"},{"key":"team","op":"!=","val":"infra"}]`,
		},
		{
			name:     "surrounding spaces are trimmed",
			selector: " env = prod ",
			want:     `[{"key":"env","op":"=","val":"prod"}]`,
		},
		{
			name:     "repeated key with opposite operators is not collapsed",
			selector: "env=prod,env!=prod",
			want:     `[{"key":"env","op":"=","val":"prod"},{"key":"env","op":"!=","val":"prod"}]`,
		},
		{
			name:     "repeated key with distinct values is not collapsed",
			selector: "env=prod,env=staging",
			want:     `[{"key":"env","op":"=","val":"prod"},{"key":"env","op":"=","val":"staging"}]`,
		},
		{
			name:     "value containing an equal sign is preserved",
			selector: "url=a=b",
			want:     `[{"key":"url","op":"=","val":"a=b"}]`,
		},
		{
			name:     "missing operator",
			selector: "env",
			wantErr:  true,
		},
		{
			name:     "whitespace only selector",
			selector: "   ",
			want:     "[]",
		},
		{
			name:     "trailing comma is ignored",
			selector: "env=prod,",
			want:     `[{"key":"env","op":"=","val":"prod"}]`,
		},
		{
			name:     "blank segments between constraints are ignored",
			selector: "env=prod, ,,team!=infra",
			want:     `[{"key":"env","op":"=","val":"prod"},{"key":"team","op":"!=","val":"infra"}]`,
		},
		{
			name:     "commas only",
			selector: ",, ,",
			want:     "[]",
		},
		{
			name:     "empty key with equality",
			selector: "=prod",
			wantErr:  true,
		},
		{
			name:     "empty key with inequality",
			selector: "env=prod, !=infra",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConnectionFilterOption{TagSelector: tt.selector}.ParseTagSelectorQuery()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseTagSelectorQuery(%q) = %v, want error", tt.selector, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTagSelectorQuery(%q) returned unexpected error: %v", tt.selector, err)
			}
			if got != tt.want {
				t.Errorf("ParseTagSelectorQuery(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}
