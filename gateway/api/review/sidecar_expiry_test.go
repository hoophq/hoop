package reviewapi

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A gateway review never shows a deadline, even with the column set, and a
// settled sidecar review shows none.
func TestToOpenApiReviewShowsTheDeadlineOnlyOnASidecarReview(t *testing.T) {
	deadline := time.Date(2026, 9, 28, 12, 15, 0, 0, time.UTC)
	keys := func(t *testing.T, rev *models.Review) map[string]any {
		t.Helper()
		body, err := json.Marshal(toOpenApiReview(rev))
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		return got
	}

	t.Run("a pending sidecar review", func(t *testing.T) {
		rev := newFakeSidecarReview("dba")
		rev.ExpiresAt, rev.ApprovalTTLSec = &deadline, ptr.Int(600)
		got := keys(t, rev)
		assert.Equal(t, "2026-09-28T12:15:00Z", got["expires_at"])
		assert.Equal(t, float64(600), got["approval_ttl_sec"])
	})

	t.Run("a gateway review", func(t *testing.T) {
		rev := newFakeReview("user-1", "PENDING", "onetime", nil, nil)
		rev.ExpiresAt, rev.ApprovalTTLSec = &deadline, ptr.Int(600)
		got := keys(t, rev)
		assert.NotContains(t, got, "expires_at")
		assert.NotContains(t, got, "approval_ttl_sec")
	})

	t.Run("a rejected sidecar review", func(t *testing.T) {
		rev := newFakeSidecarReview("dba")
		rev.Status = models.ReviewStatusRejected
		rev.ExpiresAt = &deadline
		assert.NotContains(t, keys(t, rev), "expires_at")
	})

	t.Run("a sidecar review with no limit", func(t *testing.T) {
		got := keys(t, newFakeSidecarReview("dba"))
		assert.NotContains(t, got, "expires_at")
		assert.NotContains(t, got, "approval_ttl_sec")
	})
}

// PUT /reviews/:id has no role middleware, so a caller who could not decide
// the review must not be able to record its expiry either.
func TestMaySettleSidecarReview(t *testing.T) {
	rev := newFakeSidecarReview("dba")
	rev.ForceApprovalGroups = []string{"security"}

	for _, tc := range []struct {
		name   string
		groups []string
		want   bool
	}{
		{"an admin", []string{types.GroupAdmin}, true},
		{"a review group member", []string{"engineering", "dba"}, true},
		{"a force group member", []string{"security"}, true},
		{"an outsider", []string{"engineering"}, false},
		{"a user with no groups", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newFakeContext("u1", "user@hoop.dev", tc.groups)
			assert.Equal(t, tc.want, maySettleSidecarReview(ctx, rev))
		})
	}
}

// The status is checked before anything else, and a caller who may not settle
// the review is answered before any database call.
func TestRefuseExpiredSidecarDecisionWritesNothingItNeedNot(t *testing.T) {
	rev := newFakeSidecarReview("dba")
	rev.Status = models.ReviewStatusExpired
	rev.ListenerName = sql.NullString{String: "appdb", Valid: true}
	admin := newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin})

	for _, status := range []models.ReviewStatusType{"FOO", "", models.ReviewStatusPending, models.ReviewStatusExpired} {
		assert.Equal(t, ErrUnknownStatus, refuseExpiredSidecarDecision(admin, rev, status), "status %q", status)
	}

	outsider := newFakeContext("u2", "dev@hoop.dev", []string{"engineering"})
	for _, status := range []models.ReviewStatusType{
		models.ReviewStatusApproved, models.ReviewStatusRejected, models.ReviewStatusRevoked,
	} {
		assert.Equal(t, ErrExpired, refuseExpiredSidecarDecision(outsider, rev, status), "status %q", status)
	}
}
