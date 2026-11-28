package apisidecar

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/jobs/reviewescalation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSidecarReviewEscalation(t *testing.T) {
	startStatusTestDB(t)
	noModel(t)

	t.Run("unanswered review escalates after the delay, adds the groups, DMs them once, and lets them approve", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "esc-a", "esc-a-rule", ownerSpec("esc-a-rule",
			`"escalate": {"after_sec": 600, "groups": ["risk"]}`))
		setListenerChannels(t, sc, "C-LANE")

		owner := seedUser(t, "EscOwner", "active", "U-ESC-OWNER")
		risk := seedUser(t, "EscRisk", "active", "U-ESC-RISK", "risk")
		setOwners(t, sc, owner.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM ledgers WHERE id = 1", "high"))
		waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"U-ESC-OWNER"}, slackFake.destinations())

		// Before escalation: risk member cannot see the review.
		_, err := models.GetReviewByIdOrSidForViewer(models.DB, statusTestOrgID, filed.Review.ID, reviewapi.Viewer(userCtx(risk, "risk")))
		assert.ErrorIs(t, err, models.ErrNotFound)

		// Before the delay: sweep does nothing.
		assert.Zero(t, reviewescalation.SweepOnce(context.Background(), models.DB, time.Now().UTC().Add(5*time.Minute)))

		// Past the delay: sweep escalates once and sends a DM to risk (no channel).
		assert.Equal(t, 1, reviewescalation.SweepOnce(context.Background(), models.DB, time.Now().UTC().Add(11*time.Minute)))
		assert.Equal(t, []string{"U-ESC-OWNER", "U-ESC-RISK"}, sorted(slackFake.destinations()))

		// A second sweep does not escalate again.
		assert.Zero(t, reviewescalation.SweepOnce(context.Background(), models.DB, time.Now().UTC().Add(12*time.Minute)))
		assert.Len(t, slackFake.destinations(), 2)

		// Now the risk member sees and approves the review.
		got, err := models.GetReviewByIdOrSidForViewer(models.DB, statusTestOrgID, filed.Review.ID, reviewapi.Viewer(userCtx(risk, "risk")))
		require.NoError(t, err)
		assert.Equal(t, filed.Review.ID, got.ID)

		approved, err := reviewapi.DoReview(userCtx(risk, "risk"), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, approved.Status)
	})

	t.Run("a review decided or expired before the delay does not escalate", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "esc-b", "esc-b-rule", ownerSpec("esc-b-rule",
			`"escalate": {"after_sec": 600, "groups": ["sec"]}`))
		owner := seedUser(t, "EscOwnerB", "active", "U-ESC-OWNER-B")
		seedUser(t, "EscSec", "active", "U-ESC-SEC", "sec")
		setOwners(t, sc, owner.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM ledgers WHERE id = 2", "high"))
		waitNotified(t, sc, filed.Review.ID)
		_, err := reviewapi.DoReview(userCtx(owner), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)

		// Decided review is skipped.
		assert.Zero(t, reviewescalation.SweepOnce(context.Background(), models.DB, time.Now().UTC().Add(15*time.Minute)))
		assert.Equal(t, []string{"U-ESC-OWNER-B"}, slackFake.destinations())

		// Expired review is skipped too.
		filed2 := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM ledgers WHERE id = 3", "high"))
		waitNotified(t, sc, filed2.Review.ID)
		require.NoError(t, models.DB.Exec(`UPDATE private.reviews SET expires_at = ? WHERE id = ?`,
			time.Now().UTC().Add(5*time.Minute), filed2.Review.ID).Error)
		assert.Zero(t, reviewescalation.SweepOnce(context.Background(), models.DB, time.Now().UTC().Add(15*time.Minute)))
		assert.Equal(t, []string{"U-ESC-OWNER-B", "U-ESC-OWNER-B"}, slackFake.destinations())
	})

	t.Run("handshake strips escalate for a build with approvers, refuses one without", func(t *testing.T) {
		sc := seedLevelSidecar(t, "esc-hs", "esc-hs-rule", ownerSpec("esc-hs-rule",
			`"escalate": {"after_sec": 900, "groups": ["risk"]}`))
		w := handshake(t, sc, daemon.CapabilityLevelApprovers+","+daemon.CapabilityApproverOwners)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), `"escalate"`)
		var served daemon.Config
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
		require.NotNil(t, served.Listeners[0].Analyzer.Approvers)
		assert.Equal(t, http.StatusUnprocessableEntity, handshake(t, sc, "").Code)
	})
}
