package policy

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestMetadataRuleValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata map[string][]string
		want     string
	}{
		{name: "no keys", metadata: nil, want: "metadata rule with no keys"},
		{name: "blank key", metadata: map[string][]string{" ": {"PURGE"}}, want: "empty key"},
		{name: "key without values", metadata: map[string][]string{"x-acmewire.verb": nil}, want: `key "x-acmewire.verb" with no values`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRules([]Rule{{Name: "r", Type: MatchMetadata, Metadata: tt.metadata}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if _, err := NewRules([]Rule{{Name: "r", Type: MatchMetadata, Metadata: map[string][]string{"x-acmewire.verb": {"PURGE"}}}}); err != nil {
		t.Fatalf("a well-formed metadata rule was refused: %v", err)
	}
}

// The rule ANDs keys and ORs values, and a key the statement lacks never
// matches, so a rule written for one protocol's keys stays silent on another
// lane.
func TestMetadataRuleMatching(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "no-purge", Type: MatchMetadata,
		Metadata: map[string][]string{
			"x-acmewire.verb":  {"PURGE", "AUTH"},
			"x-acmewire.actor": {"batch"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		metadata map[string]string
		deny     bool
	}{
		{name: "both keys match", metadata: map[string]string{"x-acmewire.verb": "PURGE", "x-acmewire.actor": "batch"}, deny: true},
		{name: "second value of the list", metadata: map[string]string{"x-acmewire.verb": "AUTH", "x-acmewire.actor": "batch"}, deny: true},
		{name: "one key wrong value", metadata: map[string]string{"x-acmewire.verb": "QUERY", "x-acmewire.actor": "batch"}, deny: false},
		{name: "one key missing", metadata: map[string]string{"x-acmewire.verb": "PURGE"}, deny: false},
		{name: "no metadata at all", metadata: nil, deny: false},
		{name: "value is a prefix, not equal", metadata: map[string]string{"x-acmewire.verb": "PURGEALL", "x-acmewire.actor": "batch"}, deny: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := rules.Evaluate(inspect.Statement{Protocol: "x-acmewire", Text: "PURGE orders", Metadata: tt.metadata})
			if got.Denied != tt.deny {
				t.Fatalf("Denied = %v, want %v (%s)", got.Denied, tt.deny, got.Message)
			}
			if tt.deny && got.Rule != "no-purge" {
				t.Fatalf("Rule = %q, want no-purge", got.Rule)
			}
		})
	}
}

// Both sides fold case: an operator typing the key and value by hand must not
// have to know how the codec spells them.
func TestMetadataRuleFoldsCaseOnBothSides(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "r", Type: MatchMetadata,
		Metadata: map[string][]string{"X-AcmeWire.Verb": {"Purge"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []map[string]string{
		{"x-acmewire.verb": "PURGE"},
		{"X-ACMEWIRE.VERB": "purge"},
	} {
		if got := rules.Evaluate(inspect.Statement{Metadata: metadata}); !got.Denied {
			t.Errorf("%v was not denied", metadata)
		}
	}
}

// Operations is a scope for metadata like for every type but operation: the
// same key on a statement outside the scope does not fire the rule.
func TestMetadataRuleHonorsOperationsScope(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "no-purge-deletes", Type: MatchMetadata,
		Operations: []inspect.Operation{inspect.OpDelete},
		Metadata:   map[string][]string{"x-acmewire.verb": {"PURGE"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"x-acmewire.verb": "PURGE"}
	if got := rules.Evaluate(inspect.Statement{Operation: inspect.OpDelete, Metadata: metadata}); !got.Denied {
		t.Fatal("a delete in scope was not denied")
	}
	if got := rules.Evaluate(inspect.Statement{Operation: inspect.OpSelect, Metadata: metadata}); got.Denied {
		t.Fatal("a select outside the scope was denied")
	}
}
