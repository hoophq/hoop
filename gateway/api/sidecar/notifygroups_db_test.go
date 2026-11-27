package apisidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	laia "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services/approvalrouting"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// notifySpec holds high for the static groups (a JSON list), with notify.
func notifySpec(ruleName, groups, notify string) string {
	return `{"high": "require_approval", "approval_rule": "` + ruleName + `",
		"approvers": {"high": {"type": "human", "groups": ` + groups + `, "notify": ` + notify + `}}}`
}

// noModel fails the test when a routing model is asked: a level with static
// groups asks none.
func noModel(t *testing.T) {
	t.Helper()
	t.Cleanup(approvalrouting.SetClientFactory(func(context.Context, *gorm.DB, string) (laia.LLMClient, error) {
		t.Error("a level with static groups asked the routing model")
		return nil, errors.New("no model")
	}))
}

func setListenerChannels(t *testing.T, sc *models.Sidecar, channels ...string) {
	t.Helper()
	require.NoError(t, models.SetSidecarSlackChannels(models.DB, sc.OrgID, sc.ID,
		[]models.SidecarSlackChannels{{ListenerName: "appdb", Channels: pq.StringArray(channels)}}))
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

// notifiedIDs are the ids of the users the record says were told, sorted.
func notifiedIDs(r *models.ReviewRouting) []string {
	var out []string
	for _, u := range r.NotifiedUsers {
		out = append(out, u.ID)
	}
	return sorted(out)
}

// A level with static groups and notify tells its approvers as the delivery
// says, with no model. One embedded database for every case: the groups
// differ, so the users of one case are not approvers of another.
func TestALevelWithStaticGroupsAndNotify(t *testing.T) {
	startStatusTestDB(t)
	noModel(t)

	t.Run("dm tells every approver of the groups and posts in no channel", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "dm", "dm-rule", notifySpec("dm-rule", `["dba"]`, `{"slack": {"delivery": "dm"}}`))
		setListenerChannels(t, sc, "C-LANE")
		ana := seedUser(t, "DmAna", "active", "U-ANA", "dba")
		bo := seedUser(t, "DmBo", "invited", "U-BO", "dba", "sre")
		noSlack := seedUser(t, "DmCy", "active", "", "dba")
		seedUser(t, "DmOff", "inactive", "U-OFF", "dba")
		seedUser(t, "DmSre", "active", "U-SRE", "sre")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 1", "high"))
		assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, models.ReviewRoutingModeGroups, r.Mode)
		assert.Equal(t, []string{"dba"}, r.Groups)
		assert.Empty(t, r.FallbackCause)
		assert.Equal(t, sorted([]string{ana.ID, bo.ID}), notifiedIDs(r))
		require.Len(t, r.Unreachable, 1)
		assert.Equal(t, noSlack.ID, r.Unreachable[0].UserID)
		assert.Empty(t, r.Channels)
		assert.Empty(t, r.NotifyFallback)
		assert.Equal(t, []string{"U-ANA", "U-BO"}, sorted(slackFake.destinations()))
	})

	t.Run("dm that reaches nobody still posts in no channel", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "dm-nobody", "dm-nobody-rule",
			notifySpec("dm-nobody-rule", `["ops"]`, `{"slack": {"delivery": "dm"}}`))
		setListenerChannels(t, sc, "C-LANE")
		seedUser(t, "OpsNoSlack", "active", "", "ops")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 2", "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Empty(t, r.NotifiedUsers)
		assert.Empty(t, r.Channels)
		assert.Equal(t, models.ReviewRoutingNotifyNotDelivered, r.NotifyFallback)
		assert.Equal(t, models.ReviewRoutingNotifyFallbackDM, r.NotifyFallbackDelivery)
		assert.False(t, r.NotifyFallbackPosted)
		assert.Empty(t, slackFake.destinations())
	})

	t.Run("both tells the approvers and the listener channels, not the default", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "both", "both-rule", notifySpec("both-rule", `["risk"]`, `{"slack": {"delivery": "both"}}`))
		setListenerChannels(t, sc, "C-LANE")
		eve := seedUser(t, "BothEve", "active", "U-EVE", "risk")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 3", "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		require.Eventually(t, func() bool { return len(slackFake.destinations()) == 2 }, 15*time.Second, 50*time.Millisecond)
		assert.Equal(t, []string{"C-LANE", "U-EVE"}, sorted(slackFake.destinations()))
		r = storedRouting(t, sc, filed.Review.ID)
		assert.Equal(t, []string{eve.ID}, notifiedIDs(r))
		require.Len(t, r.Channels, 1)
		assert.Equal(t, "C-LANE", r.Channels[0].ID)
	})

	t.Run("channel with no listener channel posts in the default, as a level without notify", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "chan", "chan-rule", notifySpec("chan-rule", `["audit"]`, `{"slack": {"delivery": "channel"}}`))
		seedUser(t, "ChanFay", "active", "U-FAY", "audit")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 4", "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Empty(t, r.NotifiedUsers)
		require.Len(t, r.Channels, 1)
		assert.Equal(t, "C-DEFAULT", r.Channels[0].ID)
		assert.Equal(t, []string{"C-DEFAULT"}, slackFake.destinations())
	})

	t.Run("channels_candidates name the channels of the listener that get the post", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "named", "named-rule", notifySpec("named-rule", `["legal"]`,
			`{"slack": {"delivery": "channel", "channels_candidates": ["C-LEGAL", "C-ELSEWHERE"]}}`))
		setListenerChannels(t, sc, "C-LANE", "C-LEGAL")
		seedUser(t, "NamedGus", "active", "U-GUS", "legal")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 5", "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"C-LEGAL"}, slackFake.destinations())
		// A channel the listener does not have is recorded, never posted to.
		require.Len(t, r.UndeliveredChannels, 1)
		assert.Equal(t, "C-ELSEWHERE", r.UndeliveredChannels[0].ID)
	})

	t.Run("no groups means the administrators", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "admins", "admins-rule", notifySpec("admins-rule", `[]`, `{"slack": {}}`))
		root := seedUser(t, "AdminRoot", "active", "U-ROOT", "admin")

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 6", "high"))
		assert.Equal(t, []string{"admin"}, reviewGroupNames(filed.Review))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{root.ID}, notifiedIDs(r))
		assert.Equal(t, []string{"U-ROOT"}, slackFake.destinations())
	})

	t.Run("a build that decodes approvers and not notify is served without it, and the plane still acts on it", func(t *testing.T) {
		slackFake := useSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "older", "older-rule", notifySpec("older-rule", `["treasury"]`, `{"slack": {"delivery": "dm"}}`))
		seedUser(t, "OlderHal", "active", "U-HAL", "treasury")

		w := handshake(t, sc, daemon.CapabilityLevelApprovers)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), `"notify"`)
		var served daemon.Config
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
		require.NotNil(t, served.Listeners[0].Analyzer.Approvers)
		assert.Equal(t, []string{"treasury"}, served.Listeners[0].Analyzer.Approvers.High.Groups)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 7", "high"))
		waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"U-HAL"}, slackFake.destinations())

		// A build that does not decode approvers would file under the rule,
		// and the rule posts in a channel: it is refused.
		assert.Equal(t, http.StatusUnprocessableEntity, handshake(t, sc, "").Code)
	})
}
