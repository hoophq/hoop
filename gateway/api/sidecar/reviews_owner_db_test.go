package apisidecar

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSlackDMs is a Slack API that records every chat.postMessage.
type fakeSlackDMs struct {
	mu   sync.Mutex
	sent []struct{ channel, text string }
}

func (f *fakeSlackDMs) to(channel string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var texts []string
	for _, m := range f.sent {
		if m.channel == channel {
			texts = append(texts, m.text)
		}
	}
	return texts
}

// installFakeSlack registers a Slack service for the test org that posts to
// the fake, with no default channel.
func installFakeSlack(t *testing.T) *fakeSlackDMs {
	t.Helper()
	f := &fakeSlackDMs{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path == "/chat.postMessage" {
			f.mu.Lock()
			f.sent = append(f.sent, struct{ channel, text string }{r.FormValue("channel"), r.FormValue("text")})
			f.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	svc := slackservice.NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "")
	slackservice.SetServiceInstance(statusTestOrgID, svc)
	t.Cleanup(func() {
		slackservice.RemoveServiceInstanceIf(statusTestOrgID, svc)
		srv.Close()
	})
	return f
}

// seedOwnedSidecar stores a reviewing sidecar whose owner is owner.
func seedOwnedSidecar(t *testing.T, name, owner string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     statusTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name:     "appdb",
			Analyzer: &daemon.LaneAnalyzerConfig{ApprovalRule: "payments-approvers"},
		}}},
	}
	if owner != "" {
		sc.OwnerEmail = ptr.String(owner)
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	seedApprovalRule(t)
	// As the token middleware loads it.
	loaded, err := models.GetSidecarByKeyHash(models.DB, sc.KeyHash)
	require.NoError(t, err)
	return loaded
}

// seedSlackUser stores an active user with a linked Slack ID and returns its id.
func seedSlackUser(t *testing.T, email, slackID string) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
		VALUES (?, ?, ?, ?, ?, 'active', ?)`, id, statusTestOrgID, "sub|"+id, email, "Alice", slackID).Error)
	return id
}

// postReviewAs files a statement naming principal; "" names nobody.
func postReviewAs(sc *models.Sidecar, raw []byte, principal string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	req := map[string]any{
		"listener_name": "appdb",
		"approval_rule": "payments-approvers",
		"payload":       base64.StdEncoding.EncodeToString(raw),
	}
	if principal != "" {
		req["principal"] = principal
	}
	body, _ := json.Marshal(req)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	PostReview(c)
	return rec
}

func filedReview(t *testing.T, rec *httptest.ResponseRecorder) (*models.Review, *models.Session) {
	t.Helper()
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var filed openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &filed))
	rev, err := models.GetReviewByIdOrSid(statusTestOrgID, filed.Review.ID)
	require.NoError(t, err)
	sess, err := models.GetSessionByID(statusTestOrgID, rev.SessionID)
	require.NoError(t, err)
	return rev, sess
}

// Who a review is filed under: the principal when it is an email, else the
// sidecar's owner, else the placeholder. A person with a linked Slack ID is
// messaged when the review is filed and when it is decided.
func TestSidecarReviewIsFiledUnderThePerson(t *testing.T) {
	startStatusTestDB(t)
	fake := installFakeSlack(t)
	aliceID := seedSlackUser(t, "alice@acme.com", "U-ALICE")
	sc := seedOwnedSidecar(t, "owned", "owner@acme.com")
	require.Equal(t, "owner@acme.com", ptr.ToString(sc.OwnerEmail), "the owner round-trips the token lookup")

	t.Run("an email principal is the person", func(t *testing.T) {
		rev, sess := filedReview(t, postReviewAs(sc, []byte("DELETE FROM users WHERE id = 1;"), "alice@acme.com"))
		assert.Equal(t, "alice@acme.com", rev.OwnerEmail)
		assert.Equal(t, aliceID, rev.OwnerID, "the hoop user with that email owns it")
		assert.Equal(t, "Alice", ptr.ToString(rev.OwnerName))
		assert.Equal(t, "alice@acme.com", sess.UserEmail)
		assert.Equal(t, aliceID, sess.UserID)
		assert.Equal(t, "alice@acme.com", newSlackReviewRequest(sc, rev, "appdb", "x").Email,
			"the channel card shows the person")

		require.Eventually(t, func() bool { return len(fake.to("U-ALICE")) == 1 }, 3*time.Second, 20*time.Millisecond,
			"the filed DM")
		assert.Contains(t, fake.to("U-ALICE")[0], "waiting for approval")
		assert.Contains(t, fake.to("U-ALICE")[0], "/approvals/"+rev.SessionID)

		approved := putReview(t, rev.ID, `{"status":"approved"}`, []string{"dba"})
		require.Equal(t, http.StatusOK, approved.Code, approved.Body.String())
		require.Eventually(t, func() bool { return len(fake.to("U-ALICE")) == 2 }, 3*time.Second, 20*time.Millisecond,
			"the decision DM")
		assert.Contains(t, fake.to("U-ALICE")[1], "approved")
	})

	t.Run("a denial names the reviewer and the reason", func(t *testing.T) {
		before := len(fake.to("U-ALICE"))
		rev, _ := filedReview(t, postReviewAs(sc, []byte("DELETE FROM users WHERE id = 2;"), "alice@acme.com"))
		denied := putReview(t, rev.ID, `{"status":"rejected","rejection_reason":"not now"}`, []string{"dba"})
		require.Equal(t, http.StatusOK, denied.Code, denied.Body.String())
		require.Eventually(t, func() bool { return len(fake.to("U-ALICE")) == before+2 }, 3*time.Second, 20*time.Millisecond)
		dm := fake.to("U-ALICE")[before+1]
		assert.Contains(t, dm, "denied")
		assert.Contains(t, dm, "Reviewer")
		assert.Contains(t, dm, "not now")
	})

	t.Run("a database role falls back to the owner", func(t *testing.T) {
		rev, sess := filedReview(t, postReviewAs(sc, []byte("DELETE FROM users WHERE id = 3;"), "postgres"))
		assert.Equal(t, "owner@acme.com", rev.OwnerEmail)
		assert.Equal(t, sc.ID, rev.OwnerID, "no hoop user has the owner's email, so the sidecar owns it")
		assert.Equal(t, sc.Name, ptr.ToString(rev.OwnerName))
		assert.Equal(t, "owner@acme.com", sess.UserEmail)
	})

	t.Run("no principal falls back to the owner", func(t *testing.T) {
		rev, _ := filedReview(t, postReviewAs(sc, []byte("DELETE FROM users WHERE id = 4;"), ""))
		assert.Equal(t, "owner@acme.com", rev.OwnerEmail)
	})

	t.Run("nobody named is the placeholder", func(t *testing.T) {
		unowned := seedOwnedSidecar(t, "unowned", "")
		rev, sess := filedReview(t, postReviewAs(unowned, []byte("DELETE FROM users WHERE id = 5;"), "postgres"))
		assert.Equal(t, reviewOwnerEmail, rev.OwnerEmail)
		assert.Equal(t, unowned.ID, rev.OwnerID)
		assert.Equal(t, reviewOwnerEmail, sess.UserEmail)
	})
}
