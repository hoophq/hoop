package apisidecar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/featureflag"

	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedSlackPost struct {
	Channel string
	Text    string
	Blocks  string
}

// recordingSlack records the channel, text and blocks of every chat.postMessage.
type recordingSlack struct {
	mu      sync.Mutex
	posts   []recordedSlackPost
	byEmail map[string]string
}

func (f *recordingSlack) all() []recordedSlackPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedSlackPost(nil), f.posts...)
}

func (f *recordingSlack) destinations() []string {
	var out []string
	for _, p := range f.all() {
		out = append(out, p.Channel)
	}
	return out
}

func useRecordingSlack(t *testing.T, defaultChannel string) *recordingSlack {
	t.Helper()
	f := &recordingSlack{byEmail: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/users.lookupByEmail" {
			email := strings.ToLower(r.FormValue("email"))
			f.mu.Lock()
			uid, ok := f.byEmail[email]
			f.mu.Unlock()
			if !ok {
				fmt.Fprint(w, `{"ok":false,"error":"users_not_found"}`)
				return
			}
			fmt.Fprintf(w, `{"ok":true,"user":{"id":%q,"team_id":"T1","is_email_confirmed":true,"profile":{"email":%q}}}`, uid, email)
			return
		}
		p := recordedSlackPost{
			Channel: r.FormValue("channel"),
			Text:    r.FormValue("text"),
			Blocks:  r.FormValue("blocks"),
		}
		if r.URL.Path == "/chat.postMessage" {
			f.mu.Lock()
			f.posts = append(f.posts, p)
			f.mu.Unlock()
		}
		fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"1.0"}`, p.Channel)
	}))
	t.Cleanup(srv.Close)
	svc := slackservice.NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", defaultChannel)
	slackservice.SetServiceInstance(statusTestOrgID, svc)
	t.Cleanup(func() { slackservice.RemoveServiceInstanceIf(statusTestOrgID, svc) })
	return f
}

func ownerSpec(ruleName, extra string) string {
	if extra != "" {
		extra = ", " + extra
	}
	return `{"high": "require_approval", "approval_rule": "` + ruleName + `",
		"approvers": {"high": {"type": "human", "owners": true` + extra + `}}}`
}

func setOwners(t *testing.T, sc *models.Sidecar, emails ...string) {
	t.Helper()
	lower := make([]string, len(emails))
	for i, e := range emails {
		lower[i] = strings.ToLower(e)
	}
	require.NoError(t, models.SetSidecarOwners(models.DB, sc.OrgID, sc.ID, lower))
}

func userCtx(u seededUser, groups ...string) *storagev2.Context {
	return storagev2.NewContext("sub-"+u.ID, statusTestOrgID).
		WithUserInfo(u.Name, u.Email, "active", "", groups)
}

func TestALevelWithOwners(t *testing.T) {
	startStatusTestDB(t)
	noModel(t)

	t.Run("owners alone: DM by default, no admin group, no default channel, only real owners see and approve", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "owned-a", "owned-a-rule", ownerSpec("owned-a-rule", ""))
		otherSc := seedLevelSidecar(t, "owned-b", "owned-b-rule", ownerSpec("owned-b-rule", ""))
		setListenerChannels(t, sc, "C-LANE")

		ana := seedUser(t, "OwnAna", "active", "U-OWN-ANA")
		bo := seedUser(t, "OwnBo", "active", "U-OWN-BO")
		otherOwner := seedUser(t, "OwnOther", "active", "U-OWN-OTHER")
		spoof := seedUser(t, "OwnSpoof", "active", "U-SPOOF", models.ReviewGroupSidecarOwners)
		setOwners(t, sc, ana.Email, bo.Email, "unmatched@example.com")
		setOwners(t, otherSc, otherOwner.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM secret_table WHERE id = 1", "high"))
		assert.Equal(t, []string{models.ReviewGroupSidecarOwners}, reviewGroupNames(filed.Review))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"U-OWN-ANA", "U-OWN-BO"}, sorted(slackFake.destinations()))
		assert.Equal(t, sorted([]string{ana.ID, bo.ID}), notifiedIDs(r))
		assert.Empty(t, r.Channels)

		// Visibility: ana sees it; otherOwner and spoof (real IdP group named sidecar-owners) do not.
		got, err := models.GetReviewByIdOrSidForViewer(models.DB, statusTestOrgID, filed.Review.ID, reviewapi.Viewer(userCtx(ana)))
		require.NoError(t, err)
		assert.Equal(t, filed.Review.ID, got.ID)
		assert.Equal(t, strings.ToLower(ana.Email), got.OwnerEmail)

		_, err = models.GetReviewByIdOrSidForViewer(models.DB, statusTestOrgID, filed.Review.ID, reviewapi.Viewer(userCtx(otherOwner)))
		assert.ErrorIs(t, err, models.ErrNotFound)

		_, err = models.GetReviewByIdOrSidForViewer(models.DB, statusTestOrgID, filed.Review.ID, reviewapi.Viewer(userCtx(spoof, models.ReviewGroupSidecarOwners)))
		assert.ErrorIs(t, err, models.ErrNotFound)

		// Decision: spoof is refused; ana approves it.
		_, err = reviewapi.DoReview(userCtx(spoof, models.ReviewGroupSidecarOwners), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		assert.ErrorIs(t, err, reviewapi.ErrNotFound)

		approved, err := reviewapi.DoReview(userCtx(ana), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, approved.Status)
	})

	t.Run("named channel gets one line without the statement; default channel is refused", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "owned-ch", "owned-ch-rule", ownerSpec("owned-ch-rule",
			`"notify": {"slack": {"delivery": "both", "channels_candidates": ["C-DESK", "C-DEFAULT"]}}`))
		setListenerChannels(t, sc, "C-DESK")
		eve := seedUser(t, "OwnEve", "active", "U-OWN-EVE")
		setOwners(t, sc, eve.Email)

		stmt := "DELETE FROM secret_positions WHERE account = 42"
		filed := decodeReviewResponse(t, fileLevel(sc, stmt, "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"C-DESK", "U-OWN-EVE"}, sorted(slackFake.destinations()))
		require.Len(t, r.UndeliveredChannels, 1)
		assert.Equal(t, "C-DEFAULT", r.UndeliveredChannels[0].ID)

		for _, p := range slackFake.all() {
			if p.Channel == "C-DESK" {
				assert.Contains(t, p.Text, "is waiting for the owner.")
				assert.NotContains(t, p.Text, "secret_positions")
				assert.Empty(t, p.Blocks, "channel notice carries no buttons or statement blocks")
			} else {
				assert.Equal(t, "U-OWN-EVE", p.Channel)
				assert.Contains(t, p.Blocks, "secret_positions")
				assert.Contains(t, p.Blocks, strings.ToLower(eve.Email))
				assert.NotContains(t, p.Blocks, reviewOwnerEmail)
			}
		}
	})

	t.Run("unreachable owner never falls back to a channel", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "owned-none", "owned-none-rule", ownerSpec("owned-none-rule", ""))
		setListenerChannels(t, sc, "C-LANE")
		noSlack := seedUser(t, "OwnNoSlack", "active", "")
		setOwners(t, sc, noSlack.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM secret_table WHERE id = 2", "high"))
		r := waitNotified(t, sc, filed.Review.ID)
		assert.Empty(t, slackFake.destinations())
		assert.Equal(t, models.ReviewRoutingNotifyNotDelivered, r.NotifyFallback)
		assert.Equal(t, models.ReviewRoutingNotifyFallbackDM, r.NotifyFallbackDelivery)
		assert.False(t, r.NotifyFallbackPosted)
	})

	t.Run("owners beside groups notifies and admits both", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		sc := seedLevelSidecar(t, "owned-grp", "owned-grp-rule", ownerSpec("owned-grp-rule", `"groups": ["leads"]`))
		owner := seedUser(t, "OwnG1", "active", "U-OWN-G1")
		lead := seedUser(t, "OwnG2", "active", "U-OWN-G2", "leads")
		setOwners(t, sc, owner.Email)

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM secret_table WHERE id = 3", "high"))
		assert.Equal(t, []string{models.ReviewGroupSidecarOwners, "leads"}, reviewGroupNames(filed.Review))
		waitNotified(t, sc, filed.Review.ID)
		assert.Equal(t, []string{"U-OWN-G1", "U-OWN-G2"}, sorted(slackFake.destinations()))

		approved, err := reviewapi.DoReview(userCtx(lead, "leads"), filed.Review.ID, models.ReviewStatusApproved, nil, false, "")
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, approved.Status)
	})

	t.Run("owner with no Hoop user gets DM via email lookup when flag is on; different Slack email stays silent", func(t *testing.T) {
		slackFake := useRecordingSlack(t, "C-DEFAULT")
		slackFake.byEmail["prelogin@example.com"] = "U-PRELOGIN"
		sc := seedLevelSidecar(t, "owned-pre", "owned-pre-rule", ownerSpec("owned-pre-rule", ""))
		setListenerChannels(t, sc, "C-LANE")
		setOwners(t, sc, "prelogin@example.com", "different-email@example.com")

		featureflag.Set(statusTestOrgID, featureflag.FlagSlackLookupByEmail, true)
		t.Cleanup(func() { featureflag.Set(statusTestOrgID, featureflag.FlagSlackLookupByEmail, false) })

		filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM secret_table WHERE id = 4", "high"))
		require.Eventually(t, func() bool { return len(slackFake.destinations()) == 1 }, 10*time.Second, 50*time.Millisecond)
		assert.Equal(t, []string{"U-PRELOGIN"}, slackFake.destinations())
		r := storedRouting(t, sc, filed.Review.ID)
		require.NotNil(t, r)
		assert.Empty(t, r.Channels)
	})

	t.Run("handshake strips owners for a build with approvers, refuses one without", func(t *testing.T) {
		sc := seedLevelSidecar(t, "owned-hs", "owned-hs-rule", ownerSpec("owned-hs-rule", ""))
		w := handshake(t, sc, daemon.CapabilityLevelApprovers)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), `"owners"`)
		var served daemon.Config
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
		require.NotNil(t, served.Listeners[0].Analyzer.Approvers)
		assert.Equal(t, http.StatusUnprocessableEntity, handshake(t, sc, "").Code)
	})
}
