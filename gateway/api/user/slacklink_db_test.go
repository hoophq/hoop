package userapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	userapi "github.com/hoophq/hoop/gateway/api/user"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callSlackLink runs a link handler as the user with this email. seedUser
// stores the email as the subject too.
func callSlackLink(t *testing.T, handler gin.HandlerFunc, method, email string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext(email, listTestOrgID).
		WithUserInfo("Caller", email, "active", "", nil))
	c.Request = httptest.NewRequest(method, "/api/users/self/slack/link", nil)
	handler(c)
	return rec
}

// A user gets a code only when the feature is on and Slack runs, and the
// database holds the hash of the code, never the code.
func TestSlackLinkCodeAPI(t *testing.T) {
	startListTestDB(t)
	seedUser(t, "Ana", "ana@example.com", "active", "pw")
	seedUser(t, "Bo", "bo@example.com", "active", "pw")
	require.NoError(t, models.DB.Exec(`UPDATE private.users SET slack_id = 'U-BO' WHERE email = 'bo@example.com'`).Error)

	state := func(email string) openapi.UserSlackLink {
		rec := callSlackLink(t, userapi.GetSlackLink, http.MethodGet, email)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
		var got openapi.UserSlackLink
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		return got
	}
	codes := func() (n int64) {
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.slack_link_codes`).Scan(&n).Error)
		return n
	}

	svc := slackservice.NewWithAPIClient(slack.New("xoxb-test"), "T1", "")
	for name, setup := range map[string]func(){
		"the flag is off":         func() { slackservice.SetServiceInstance(listTestOrgID, svc) },
		"slack is not integrated": func() { featureflag.Set(listTestOrgID, featureflag.FlagSlackConnectCode, true) },
		"neither is in place":     func() {},
	} {
		t.Run("no code when "+name, func(t *testing.T) {
			setup()
			defer slackservice.RemoveServiceInstanceIf(listTestOrgID, svc)
			defer featureflag.Set(listTestOrgID, featureflag.FlagSlackConnectCode, false)

			assert.Equal(t, openapi.UserSlackLink{Available: false, Linked: false}, state("ana@example.com"))
			rec := callSlackLink(t, userapi.PostSlackLink, http.MethodPost, "ana@example.com")
			assert.Equal(t, http.StatusPreconditionFailed, rec.Code, "body: %s", rec.Body)
			assert.Zero(t, codes())
		})
	}

	slackservice.SetServiceInstance(listTestOrgID, svc)
	t.Cleanup(func() { slackservice.RemoveServiceInstanceIf(listTestOrgID, svc) })
	featureflag.Set(listTestOrgID, featureflag.FlagSlackConnectCode, true)
	t.Cleanup(func() { featureflag.Set(listTestOrgID, featureflag.FlagSlackConnectCode, false) })

	t.Run("the state says who is linked", func(t *testing.T) {
		assert.Equal(t, openapi.UserSlackLink{Available: true, Linked: false}, state("ana@example.com"))
		assert.Equal(t, openapi.UserSlackLink{Available: true, Linked: true}, state("bo@example.com"))
	})

	t.Run("a code is made for the caller and only its hash is stored", func(t *testing.T) {
		rec := callSlackLink(t, userapi.PostSlackLink, http.MethodPost, "ana@example.com")
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body)
		var got openapi.UserSlackLinkCode
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Len(t, got.Code, 8)
		assert.Equal(t, "/hoop link "+got.Code, got.Command)
		assert.WithinDuration(t, time.Now().Add(10*time.Minute), got.ExpiresAt, time.Minute)

		var stored struct {
			Email    string
			CodeHash string
		}
		require.NoError(t, models.DB.Raw(`
			SELECT u.email, c.code_hash FROM private.slack_link_codes c
			JOIN private.users u ON u.id = c.user_id`).Scan(&stored).Error)
		assert.Equal(t, "ana@example.com", stored.Email)
		assert.Len(t, stored.CodeHash, 64)
		assert.NotContains(t, stored.CodeHash, got.Code)
		assert.EqualValues(t, 1, codes())
	})
}
