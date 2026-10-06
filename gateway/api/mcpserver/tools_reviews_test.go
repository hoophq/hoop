package mcpserver

import (
	"database/sql"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		{models.ReviewStatusExpired, true},
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := isReviewTerminal(tc.status); got != tc.want {
				t.Errorf("isReviewTerminal(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// A gateway review never carries the deadline keys, even with the fields forced.
func TestReviewToMapAddsTheDeadlineOnlyWhenSet(t *testing.T) {
	deadline := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	ttl := 600
	review := func(listener string, status models.ReviewStatusType) *models.Review {
		return &models.Review{
			ID:             "rev-1",
			SessionID:      "sid-1",
			Type:           models.ReviewTypeOneTime,
			Status:         status,
			CreatedAt:      deadline.Add(-time.Hour),
			ListenerName:   sql.NullString{String: listener, Valid: listener != ""},
			ExpiresAt:      &deadline,
			ApprovalTTLSec: &ttl,
		}
	}
	keys := func(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }
	base := []string{"created_at", "id", "session", "status", "type"}

	if got := keys(reviewToMap(review("", models.ReviewStatusPending))); !slices.Equal(got, base) {
		t.Errorf("gateway review keys = %v, want %v", got, base)
	}

	unset := review("appdb", models.ReviewStatusPending)
	unset.ExpiresAt, unset.ApprovalTTLSec = nil, nil
	if got := keys(reviewToMap(unset)); !slices.Equal(got, base) {
		t.Errorf("sidecar review without limits keys = %v, want %v", got, base)
	}

	m := reviewToMap(review("appdb", models.ReviewStatusPending))
	if got, ok := m["expires_at"].(*time.Time); !ok || !got.Equal(deadline) {
		t.Errorf("expires_at = %v, want %v", m["expires_at"], deadline)
	}
	if m["approval_ttl_sec"] != 600 {
		t.Errorf("approval_ttl_sec = %v, want 600", m["approval_ttl_sec"])
	}

	// a refused review has no deadline left to show
	if _, ok := reviewToMap(review("appdb", models.ReviewStatusRejected))["expires_at"]; ok {
		t.Errorf("a rejected review shows expires_at")
	}
}

// reviews_update answers a refused decision as a tool error, not a Go error.
func TestReviewsUpdateAnswersExpired(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{reviewapi.ErrExpired, "review expired"},
		{reviewapi.ErrWrongState, "review is in wrong state"},
		{reviewapi.ErrForbidden, "access denied"},
		{reviewapi.ErrNotFound, "review not found"},
	} {
		res := reviewsUpdateRefusal(tt.err)
		if res == nil || !res.IsError || len(res.Content) != 1 {
			t.Fatalf("reviewsUpdateRefusal(%v) = %+v, want one error text", tt.err, res)
		}
		if text, ok := res.Content[0].(*mcp.TextContent); !ok || text.Text != tt.want {
			t.Errorf("reviewsUpdateRefusal(%v) text = %+v, want %q", tt.err, res.Content[0], tt.want)
		}
	}
	for _, err := range []error{nil, errors.New("db down")} {
		if res := reviewsUpdateRefusal(err); res != nil {
			t.Errorf("reviewsUpdateRefusal(%v) = %+v, want nil", err, res)
		}
	}
}
