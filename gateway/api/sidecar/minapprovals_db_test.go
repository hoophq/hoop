package apisidecar

import (
	"testing"

	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSidecarLevelMultiGroupApprovals(t *testing.T) {
	startStatusTestDB(t)
	noModel(t)

	t.Run("owners + leads with all_groups_must_approve requires both approvals before APPROVED", func(t *testing.T) {
		useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "pair-a", "pair-a-rule", ownerSpec("pair-a-rule",
			`"groups": ["leads"], "all_groups_must_approve": true`))
		owner := seedUser(t, "PairOwner", "active", "U-PAIR-OWN")
		lead := seedUser(t, "PairLead", "active", "U-PAIR-LEAD", "leads")
		setOwners(t, sc, owner.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM accounts WHERE id = 1", "high"))
		require.NotNil(t, filed.Review.MinApprovals)
		assert.Equal(t, 2, *filed.Review.MinApprovals)
		waitNotified(t, sc, filed.Review.ID)

		// First approval (owner) leaves the review PENDING.
		afterFirst, err := reviewapi.DoReview(userCtx(owner), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusPending, afterFirst.Status)

		// Second approval (lead) settles it to APPROVED.
		afterSecond, err := reviewapi.DoReview(userCtx(lead, "leads"), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, afterSecond.Status)
	})

	t.Run("min_approvals: 2 across 3 static groups settles on the second distinct group", func(t *testing.T) {
		useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "min2", "min2-rule",
			`{"high": "require_approval", "approval_rule": "min2-rule",
			  "approvers": {"high": {"type": "human", "groups": ["g1", "g2", "g3"], "min_approvals": 2}}}`)
		u1 := seedUser(t, "MinU1", "active", "U-MIN-1", "g1")
		u2 := seedUser(t, "MinU2", "active", "U-MIN-2", "g2")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM accounts WHERE id = 2", "high"))
		require.NotNil(t, filed.Review.MinApprovals)
		assert.Equal(t, 2, *filed.Review.MinApprovals)

		r1, err := reviewapi.DoReview(userCtx(u1, "g1"), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusPending, r1.Status)

		r2, err := reviewapi.DoReview(userCtx(u2, "g2"), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, r2.Status)
	})
}
