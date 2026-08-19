package sessionapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// The precondition is checked in two places — here, by a caller about to
// persist a session, and again inside CreateReviewFromAIAnalysis. It has to
// mean the same thing in both, or a caller that validates first still gets an
// orphaned session when creation rejects the rule for its own reasons.
func TestValidateAccessRuleForReview(t *testing.T) {
	tests := []struct {
		name    string
		rule    *models.AccessRequestRule
		wantErr error
	}{
		{"nil rule", nil, nil},
		{"no reviewers", &models.AccessRequestRule{Name: "appdb"}, ErrNoReviewersConfigured},
		{"one reviewer group", &models.AccessRequestRule{
			Name: "appdb", ReviewersGroups: []string{"sre"}}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAccessRuleForReview(tc.rule)
			switch {
			case tc.name == "one reviewer group":
				if err != nil {
					t.Fatalf("a rule with a reviewer group was rejected: %v", err)
				}
			case err == nil:
				t.Fatal("an unusable rule was accepted")
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
		})
	}

	// The rule name reaches the message: an operator reading a 422 has to
	// know which rule to go fix.
	err := ValidateAccessRuleForReview(&models.AccessRequestRule{Name: "payments-db"})
	if err == nil || !strings.Contains(err.Error(), "payments-db") {
		t.Errorf("error does not name the rule: %v", err)
	}
}
