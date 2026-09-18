package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

// TestValidateGuardrailRuleForSidecar pins the whole refusal table. Each row is
// a rule an admin can save today and that would reach a sidecar as nothing at
// all, or take the sidecar's whole configuration down.
func TestValidateGuardrailRuleForSidecar(t *testing.T) {
	const okInput = `{"rules":[{"type":"deny_words_list","words":["DROP TABLE"]}]}`

	for _, tt := range []struct {
		name   string
		input  string
		output string
		want   string // substring of the refusal; empty means the rule is accepted
	}{
		{
			name:  "a deny words rule both engines speak",
			input: okInput,
		},
		{
			name:  "a pattern RE2 accepts",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":"(?i)^\\s*DELETE\\b"}]}`,
		},
		{
			// The sidecar has no output guardrail: it denies requests and masks
			// responses. Serving one as a request rule would deny statements the
			// admin meant to redact.
			name:   "an output rule has no sidecar equivalent",
			input:  okInput,
			output: `{"rules":[{"type":"pattern_match","pattern_regex":"[0-9]+"}]}`,
			want:   "output rules",
		},
		{
			// Six of the sidecar's eight rule types are unknown to the gateway
			// page. Storing one would ship {"type":"table","words":null} to an
			// agent, which matches nothing forever.
			name:  "a rule type only the sidecar knows",
			input: `{"rules":[{"type":"table","words":["customers"]}]}`,
			want:  "rule type",
		},
		{
			// The sidecar compiles patterns when it LOADS the document, so one
			// bad pattern refuses the whole configuration. The gateway compiles
			// at match time, which is why this saves clean today. Five of the
			// nine seeded rulepacks carry exactly this.
			name:  "a PCRE lookahead RE2 cannot compile",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":"(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
			want:  "RE2",
		},
		{
			name:  "a deny words rule with no words",
			input: `{"rules":[{"type":"deny_words_list","words":[]}]}`,
			want:  "no words",
		},
		{
			name:  "a pattern rule with no pattern",
			input: `{"rules":[{"type":"pattern_match","pattern_regex":""}]}`,
			want:  "no pattern",
		},
		{
			name:  "a rule with nothing on the input side",
			input: `{"rules":[]}`,
			want:  "no input rules",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGuardrailRuleForSidecar("my-rule", json.RawMessage(tt.input), json.RawMessage(tt.output))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want the rule accepted, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want a refusal naming %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("refusal does not name %q: %v", tt.want, err)
			}
			// Whatever the verdict, the message names the rule, because an
			// admin reading it has several.
			if err != nil && !strings.Contains(err.Error(), "my-rule") {
				t.Errorf("refusal does not name the rule: %v", err)
			}
		})
	}
}

// TestGuardrailRulesToPolicy pins the translation itself: what the sidecar
// receives for a rule the gateway stored.
func TestGuardrailRulesToPolicy(t *testing.T) {
	rules, err := guardrailRulesToPolicy("no-drop", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["DROP TABLE"],"message":"ask the data team"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want one rule, got %d", len(rules))
	}
	r := rules[0]
	// The name reaches the sidecar's audit rows and its deny message, so it
	// carries the gateway rule's name rather than an index.
	if r.Name != "no-drop" {
		t.Errorf("name = %q, want no-drop", r.Name)
	}
	if string(r.Type) != "deny_words_list" {
		t.Errorf("type = %q", r.Type)
	}
	if len(r.Words) != 1 || r.Words[0] != "DROP TABLE" {
		t.Errorf("words = %v", r.Words)
	}
	if r.Message != "ask the data team" {
		t.Errorf("message = %q, want the operator's own", r.Message)
	}

	// One gateway rule holding several entries becomes several sidecar rules,
	// and each needs its own name: the audit row and the max_calls budget key
	// on it, so two rules sharing a name report as one.
	many, err := guardrailRulesToPolicy("mixed", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["A"]},{"type":"deny_words_list","words":["B"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(many) != 2 || many[0].Name == many[1].Name {
		t.Fatalf("want two distinctly named rules, got %+v", many)
	}
}

// TestAppendGuardrailsPreservesTheOptOut guards the one merge subtlety that is
// invisible in the type. `rules: []` is how a lane says "none of the defaults";
// composing must add to it, never replace it, or a lane that opted out silently
// starts enforcing again.
func TestAppendGuardrailsPreservesTheOptOut(t *testing.T) {
	// An opt-out is an empty but NON-nil slice: that is exactly the
	// distinction the daemon's own merge reads, and it does not survive a
	// length test.
	optedOut := &daemon.GuardrailsConfig{Rules: []policy.Rule{}}
	added, err := guardrailRulesToPolicy("r", json.RawMessage(
		`{"rules":[{"type":"deny_words_list","words":["X"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := appendGuardrails(optedOut, added)
	if len(out.Rules) != 1 {
		t.Fatalf("want the bound rule appended to the opt-out, got %d rules", len(out.Rules))
	}
	// The caller's block must not be mutated: the stored row is shared with
	// whatever else reads it this request.
	if len(optedOut.Rules) != 0 {
		t.Errorf("the source block was mutated: %+v", optedOut.Rules)
	}

	// An absent block is created rather than skipped.
	fresh := appendGuardrails(nil, added)
	if fresh == nil || len(fresh.Rules) != 1 {
		t.Errorf("want a block created for a listener that had none, got %+v", fresh)
	}
}
