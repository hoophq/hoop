package mcpserver

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func TestIsReviewTerminal(t *testing.T) {
	tests := []struct {
		status models.ReviewStatusType
		want   bool
	}{
		{models.ReviewStatusPending, false},
		{models.ReviewStatusProcessing, false},
		{models.ReviewStatusUnknown, false},
		{models.ReviewStatusApproved, true},
		{models.ReviewStatusRejected, true},
		{models.ReviewStatusRevoked, true},
		{models.ReviewStatusExecuted, true},
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := isReviewTerminal(tc.status); got != tc.want {
				t.Errorf("isReviewTerminal(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
