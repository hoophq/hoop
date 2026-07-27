package appconfig

import "testing"

// HasGuardrailProvider truth table. The predicate is informational only
// (guardrails are enforced by the built-in pattern-matching engine and are
// never gated on a provider): it reports whether a full MSPresidio
// deployment is configured, so only mspresidio with BOTH URLs qualifies.
func TestHasGuardrailProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provider   string
		analyzer   string
		anonymizer string
		want       bool
	}{
		{"mspresidio with both urls", "mspresidio", "http://a:5002", "http://a:5001", true},
		{"mspresidio missing anonymizer", "mspresidio", "http://a:5002", "", false},
		{"mspresidio missing analyzer", "mspresidio", "", "http://a:5001", false},
		{"gcp cannot enforce guardrails", "gcp", "", "", false},
		{"no provider", "", "", "", false},
		{"urls without provider", "", "http://a:5002", "http://a:5001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				dlpProvider:             tc.provider,
				msPresidioAnalyzerURL:   tc.analyzer,
				msPresidioAnonymizerURL: tc.anonymizer,
			}
			if got := c.HasGuardrailProvider(); got != tc.want {
				t.Errorf("HasGuardrailProvider() = %v, want %v", got, tc.want)
			}
		})
	}
}
