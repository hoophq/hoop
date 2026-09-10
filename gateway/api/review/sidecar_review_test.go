package reviewapi

import (
	"database/sql"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

// newFakeSidecarReview mirrors what the create path will persist: a review
// bound to a listener, with one pending group row per eligible role and no
// connection, no rule and no minimum.
func newFakeSidecarReview(groups ...string) *models.Review {
	reviewGroups := make([]models.ReviewGroups, 0, len(groups))
	for _, name := range groups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			GroupName: name,
			Status:    models.ReviewStatusPending,
		})
	}
	return &models.Review{
		ID:           "review-1",
		SessionID:    "session-1",
		OwnerID:      "sidecar-1",
		OwnerEmail:   "hoop@hoop.dev",
		Status:       models.ReviewStatusPending,
		Type:         models.ReviewTypeOneTime,
		SidecarID:    sql.NullString{String: "sidecar-1", Valid: true},
		ListenerName: sql.NullString{String: "appdb", Valid: true},
		ReviewGroups: reviewGroups,
	}
}

// One approval settles it, from either role. This is the whole policy, so it is
// the test that fails if anyone makes it configurable by accident.
func TestDoSidecarReviewSettlesOnOneApproval(t *testing.T) {
	for _, group := range []string{types.GroupAdmin, types.GroupApprover} {
		t.Run(group, func(t *testing.T) {
			rev := newFakeSidecarReview(types.GroupAdmin, types.GroupApprover)
			ctx := newFakeContext("u1", "reviewer@hoop.dev", []string{group})

			got, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)

			assert.NoError(t, err)
			assert.Equal(t, models.ReviewStatusApproved, got.Status,
				"one approval from %s must settle the review", group)
		})
	}
}

// The reviewer's own group is stamped; the other stays pending, so the record
// says who actually decided rather than implying both roles signed off.
func TestDoSidecarReviewStampsOnlyTheReviewersGroup(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin, types.GroupApprover)
	ctx := newFakeContext("u1", "reviewer@hoop.dev", []string{types.GroupApprover})

	got, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)
	assert.NoError(t, err)

	for _, rg := range got.ReviewGroups {
		if rg.GroupName == types.GroupApprover {
			assert.Equal(t, models.ReviewStatusApproved, rg.Status)
			assert.Equal(t, "reviewer@hoop.dev", *rg.OwnerEmail)
			assert.NotNil(t, rg.ReviewedAt)
			continue
		}
		assert.Equal(t, models.ReviewStatusPending, rg.Status,
			"a group that did not review must stay pending")
	}
}

// A rejection settles immediately too: there is no minimum to reach either way.
func TestDoSidecarReviewRejects(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin, types.GroupApprover)
	ctx := newFakeContext("u1", "reviewer@hoop.dev", []string{types.GroupAdmin})

	got, err := doSidecarReview(ctx, rev, models.ReviewStatusRejected)

	assert.NoError(t, err)
	assert.Equal(t, models.ReviewStatusRejected, got.Status)
}

// Membership in some unrelated group is not eligibility. Without this the gate
// is decoration.
func TestDoSidecarReviewRefusesAnIneligibleUser(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin, types.GroupApprover)
	ctx := newFakeContext("u1", "dev@hoop.dev", []string{"engineering"})

	_, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)

	assert.ErrorIs(t, err, ErrNotEligible)
	assert.Equal(t, models.ReviewStatusPending, rev.Status)
}

// A group row this policy does not recognise cannot approve, even if the
// reviewer belongs to it. Only admin and approver settle a sidecar review.
func TestDoSidecarReviewIgnoresAnUnrecognisedGroupRow(t *testing.T) {
	rev := newFakeSidecarReview("sre-team")
	ctx := newFakeContext("u1", "sre@hoop.dev", []string{"sre-team"})

	_, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)

	assert.ErrorIs(t, err, ErrNotEligible)
}

// No access window is stamped. RevokedAt is the expiry of a just-in-time grant,
// and this authorizes one statement that was already named.
func TestDoSidecarReviewSetsNoAccessWindow(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin)
	ctx := newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin})

	got, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)

	assert.NoError(t, err)
	assert.Nil(t, got.RevokedAt)
}

// A settled review cannot be reviewed again, which is validateReviewStatusTransition
// doing its job through the sidecar path too.
func TestDoSidecarReviewRefusesASettledReview(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin)
	rev.Status = models.ReviewStatusRejected
	ctx := newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin})

	_, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)

	assert.ErrorIs(t, err, ErrWrongState)
}

// IsSidecarReview is the discriminator DoReview branches on, so it decides
// whether a review ever reaches a connection lookup that would fail.
func TestIsSidecarReview(t *testing.T) {
	assert.True(t, newFakeSidecarReview(types.GroupAdmin).IsSidecarReview())

	var nilReview *models.Review
	assert.False(t, nilReview.IsSidecarReview())
	assert.False(t, (&models.Review{}).IsSidecarReview(),
		"a review with no listener must take the connection path")
	assert.False(t, (&models.Review{ListenerName: sql.NullString{String: "", Valid: true}}).IsSidecarReview(),
		"a valid-but-empty listener name is not a sidecar review")
}

// Deleting a sidecar nulls reviews.sidecar_id through the foreign key. The
// review must still be a sidecar review afterwards: if the discriminator
// flipped, the review would fall to the connection path, find no connection,
// and be impossible to settle for the rest of its life.
func TestIsSidecarReviewSurvivesSidecarDeletion(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin, types.GroupApprover)
	rev.SidecarID = sql.NullString{} // ON DELETE SET NULL

	assert.True(t, rev.IsSidecarReview(),
		"an orphaned sidecar review must keep taking the sidecar path")

	ctx := newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin})
	got, err := doSidecarReview(ctx, rev, models.ReviewStatusApproved)
	assert.NoError(t, err)
	assert.Equal(t, models.ReviewStatusApproved, got.Status)
}

// A sidecar review without a session cannot be persisted: UpdateReview syncs
// the session status and private.sessions.id is a uuid, so an empty id fails
// on a cast. Refuse it here, where the message names the real problem.
func TestDoSidecarReviewRefusesAReviewWithNoSession(t *testing.T) {
	rev := newFakeSidecarReview(types.GroupAdmin)
	rev.SessionID = ""
	ctx := newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin})

	_, err := DoSidecarReview(ctx, rev, models.ReviewStatusApproved, "")

	assert.ErrorContains(t, err, "has no session")
	assert.Equal(t, models.ReviewStatusPending, rev.Status,
		"a refused review must not be mutated")
}
