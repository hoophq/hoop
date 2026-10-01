package policy

import (
	"strings"
	"testing"
)

// RuleTypes is what a sidecar reports to its control plane, so a type the
// switch in newRules constructs but the list omits is one the plane refuses
// to serve to a build that decodes it.
func TestRuleTypesMatchNewRules(t *testing.T) {
	for _, mt := range RuleTypes() {
		_, err := newRules([]Rule{{Name: "r", Type: mt}}, true)
		if err != nil && strings.Contains(err.Error(), "unknown rule type") {
			t.Errorf("%s is listed in RuleTypes but newRules does not construct it", mt)
		}
	}
	_, err := newRules([]Rule{{Name: "r", Type: "future"}}, true)
	if err == nil || !strings.Contains(err.Error(), "unknown rule type") {
		t.Errorf("an unlisted type was not refused: %v", err)
	}
}
