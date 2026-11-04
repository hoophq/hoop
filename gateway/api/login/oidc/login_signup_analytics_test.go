package loginoidcapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	segment "github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedAnalytics logs what every analytics client a sign-in builds is
// given. A message given to a client after its Close is dropped, as the real
// client drops it.
type recordedAnalytics struct {
	mu      sync.Mutex
	msgs    []segment.Message
	dropped []segment.Message
	closes  int
}

type recordedClient struct {
	log    *recordedAnalytics
	closed bool
}

func (c *recordedClient) Enqueue(m segment.Message) error {
	c.log.mu.Lock()
	defer c.log.mu.Unlock()
	if c.closed {
		c.log.dropped = append(c.log.dropped, m)
		return segment.ErrClosed
	}
	c.log.msgs = append(c.log.msgs, m)
	return nil
}

func (c *recordedClient) Close() error {
	c.log.mu.Lock()
	defer c.log.mu.Unlock()
	c.closed = true
	c.log.closes++
	return nil
}

func (r *recordedAnalytics) newClient() *analytics.Segment {
	return &analytics.Segment{Client: &recordedClient{log: r}}
}

// waitFor returns the messages once the sign-in closed wantCloses clients,
// and fails when any message was dropped.
func (r *recordedAnalytics) waitFor(t *testing.T, wantCloses int) []segment.Message {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.closes >= wantCloses
	}, 5*time.Second, 10*time.Millisecond, "expected %d analytics clients to be closed", wantCloses)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.dropped, "messages given to a closed client are lost")
	return append([]segment.Message(nil), r.msgs...)
}

// track runs the sign-in analytics and returns what was sent once wantCloses
// clients were closed.
func track(t *testing.T, isNewUser bool, ctx *models.Context, wantCloses int) []segment.Message {
	t.Helper()
	rec := &recordedAnalytics{}
	h := &handler{newAnalytics: rec.newClient}
	h.analyticsTrack(isNewUser, "test-agent", ctx)
	if wantCloses == 0 {
		time.Sleep(200 * time.Millisecond)
	}
	return rec.waitFor(t, wantCloses)
}

func identifyOf(msgs []segment.Message) *segment.Identify {
	for _, m := range msgs {
		if id, ok := m.(segment.Identify); ok {
			return &id
		}
	}
	return nil
}

func tracksOf(msgs []segment.Message, event string) []segment.Track {
	var out []segment.Track
	for _, m := range msgs {
		if tr, ok := m.(segment.Track); ok && tr.Event == event {
			out = append(out, tr)
		}
	}
	return out
}

func noSignupDelay(t *testing.T) {
	delay := signupTrackDelay
	signupTrackDelay = 0
	t.Cleanup(func() { signupTrackDelay = delay })
}

// On a multi-tenant gateway the callback loads the user context before the new
// user and org exist. The signup analytics must use the context reloaded after
// registration, or Identify is skipped and the email never reaches analytics.
func TestReloadSignupContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))

	t.Run("a new multi-tenant user gets their new org, email and analytics mode", func(t *testing.T) {
		stale, err := models.GetUserContext("idp|erin")
		require.NoError(t, err)
		require.True(t, stale.IsEmpty(), "the user must not exist before the sign-up")

		isNewUser, err := registerMultiTenantUser(idptypes.ProviderUserInfo{
			Subject: "idp|erin", Email: "erin@acme.test", Profile: "Erin"}, "")
		require.NoError(t, err)
		require.True(t, isNewUser)

		ctx := reloadSignupContext(stale, "idp|erin")
		assert.NotEmpty(t, ctx.OrgID)
		assert.Equal(t, "idp|erin", ctx.UserSubject)
		assert.Equal(t, "erin@acme.test", ctx.UserEmail)
		assert.Equal(t, "Erin", ctx.UserName)
		assert.Equal(t, models.AnalyticsModeIdentified, ctx.OrgAnalyticsMode)
	})

	t.Run("an invited user's first sign-in is not a new sign-up", func(t *testing.T) {
		// The invite already sent hoop-signup; counting the first sign-in
		// again would count the teammate twice.
		orgID := uuid.NewString()
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'invite-org')`, orgID).Error)
		require.NoError(t, models.DB.Exec(`
			INSERT INTO private.users (org_id, subject, email, name, status)
			VALUES (?, 'gil@acme.test', 'gil@acme.test', 'Gil', 'invited')`, orgID).Error)

		isNewUser, err := registerMultiTenantUser(idptypes.ProviderUserInfo{
			Subject: "idp|gil", Email: "gil@acme.test", Profile: "Gil"}, "")
		require.NoError(t, err)
		assert.False(t, isNewUser)

		ctx := reloadSignupContext(&models.Context{}, "idp|gil")
		assert.Equal(t, orgID, ctx.OrgID)
		assert.Equal(t, "idp|gil", ctx.UserSubject)
	})

	t.Run("an unknown subject keeps the context it was given", func(t *testing.T) {
		stale := &models.Context{UserEmail: "kept@acme.test"}
		ctx := reloadSignupContext(stale, "idp|nobody")
		assert.Same(t, stale, ctx)
	})
}

// A new sign-up reaches analytics as one person: the Identify carries the
// email only when the org's mode allows it, it is flushed at once, and
// hoop-signup goes out on a client that is still open.
func TestAnalyticsTrackSignup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	noSignupDelay(t)

	isNewUser, err := registerMultiTenantUser(idptypes.ProviderUserInfo{
		Subject: "idp|fay", Email: "fay@acme.test", Profile: "Fay"}, "")
	require.NoError(t, err)
	require.True(t, isNewUser)
	ctx := reloadSignupContext(&models.Context{}, "idp|fay")
	require.NotEmpty(t, ctx.OrgID)

	t.Run("identified mode: the email goes out and hoop-signup follows", func(t *testing.T) {
		msgs := track(t, true, ctx, 2)

		id := identifyOf(msgs)
		require.NotNil(t, id, "the sign-up must be identified")
		assert.Equal(t, "fay@acme.test", id.Traits["email"])
		assert.Equal(t, ctx.OrgID, id.Traits["org-id"])

		signups := tracksOf(msgs, analytics.EventSignup)
		require.Len(t, signups, 1)
		assert.Equal(t, ctx.OrgID, signups[0].Properties["org-id"])
		assert.Equal(t, id.UserId, signups[0].UserId, "the event and the person must share one id")
		assert.NotContains(t, signups[0].Properties, "email")
	})

	t.Run("the Identify is flushed before the signup wait", func(t *testing.T) {
		signupTrackDelay = time.Hour
		t.Cleanup(func() { signupTrackDelay = 0 })
		rec := &recordedAnalytics{}
		(&handler{newAnalytics: rec.newClient}).analyticsTrack(true, "test-agent", ctx)

		msgs := rec.waitFor(t, 1)
		assert.NotNil(t, identifyOf(msgs))
		assert.Empty(t, tracksOf(msgs, analytics.EventSignup))
	})

	t.Run("anonymous mode: hashed id only, no email", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeAnonymous))
		anonCtx := reloadSignupContext(ctx, "idp|fay")
		require.Equal(t, models.AnalyticsModeAnonymous, anonCtx.OrgAnalyticsMode)

		msgs := track(t, true, anonCtx, 2)

		id := identifyOf(msgs)
		require.NotNil(t, id)
		assert.NotContains(t, id.Traits, "email")
		assert.NotContains(t, id.Traits, "name")
		assert.Len(t, tracksOf(msgs, analytics.EventSignup), 1)
	})

	t.Run("disabled mode: nothing goes out, whatever the mode cache holds", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeDisabled))
		offCtx := reloadSignupContext(ctx, "idp|fay")
		require.Equal(t, models.AnalyticsModeDisabled, offCtx.OrgAnalyticsMode)
		analytics.SetMode(offCtx.OrgID, models.AnalyticsModeIdentified) // a stale cache entry

		assert.Empty(t, track(t, true, offCtx, 0))
		assert.Equal(t, models.AnalyticsModeIdentified, analytics.GetMode(offCtx.OrgID),
			"the sign-in must not write the mode cache")
	})

	t.Run("no org: nothing goes out", func(t *testing.T) {
		assert.Empty(t, track(t, true, &models.Context{UserSubject: "idp|fay"}, 0))
	})

	t.Run("a returning user's hoop-login still goes out", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeIdentified))
		loginCtx := reloadSignupContext(ctx, "idp|fay")

		msgs := track(t, false, loginCtx, 1)
		assert.Nil(t, identifyOf(msgs))
		assert.Len(t, tracksOf(msgs, analytics.EventLogin), 1)
	})
}

const multiTenantChildEnv = "HOOP_TEST_MULTITENANT_CHILD"

// The whole multi-tenant callback, from the identity provider's answer to
// what reaches the analytics client. The gateway reads ORG_MULTI_TENANT once
// per process, so the test runs itself again in a child process.
func TestMultiTenantSignupCallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	if os.Getenv(multiTenantChildEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMultiTenantSignupCallback$", "-test.v")
		cmd.Env = append(os.Environ(), multiTenantChildEnv+"=1", "ORG_MULTI_TENANT=true")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "multi-tenant child run failed:\n%s", out)
		// A child that matched no test would also exit 0.
		for _, sub := range []string{"a_new_self-serve_sign-up", "an_invited_teammate"} {
			assert.Contains(t, string(out), "--- PASS: TestMultiTenantSignupCallback/"+sub)
		}
		return
	}

	require.NoError(t, appconfig.Load())
	require.True(t, appconfig.Get().OrgMultitenant())
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	noSignupDelay(t)

	// An org whose admin chose anonymous analytics, with one invited teammate.
	anonOrg := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name, analytics_mode) VALUES (?, 'anon-org', 'anonymous')`, anonOrg).Error)
	require.NoError(t, models.DB.Exec(`
		INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, 'ana@acme.test', 'ana@acme.test', 'Ana', 'invited')`, anonOrg).Error)
	analytics.WarmModeCache() // as the gateway does at boot

	signInWith := func(t *testing.T, rec *recordedAnalytics, uinfo idptypes.ProviderUserInfo) {
		t.Helper()
		state := uuid.NewString()
		require.NoError(t, models.CreateLogin(&models.Login{ID: state, Redirect: "http://localhost:8009/callback"}))
		gin.SetMode(gin.TestMode)
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/callback?code=the-code&state="+state, nil)
		h := &handler{
			newVerifier:  func() (idp.OidcVerifier, error) { return fakeVerifier{uinfo: uinfo}, nil },
			newAnalytics: rec.newClient,
		}
		h.LoginCallback(c)
		require.Equal(t, http.StatusTemporaryRedirect, resp.Code, resp.Body.String())
		require.NotContains(t, resp.Header().Get("Location"), "error=")
	}

	t.Run("a new self-serve sign-up is identified with its email, then hoop-signup", func(t *testing.T) {
		rec := &recordedAnalytics{}
		signInWith(t, rec, idptypes.ProviderUserInfo{Subject: "idp|newdev", Email: "newdev@acme.test", Profile: "New Dev"})
		msgs := rec.waitFor(t, 2)

		ctx, err := models.GetUserContext("idp|newdev")
		require.NoError(t, err)
		require.NotEmpty(t, ctx.OrgID)

		id := identifyOf(msgs)
		require.NotNil(t, id)
		assert.Equal(t, "newdev@acme.test", id.Traits["email"])
		assert.Equal(t, ctx.OrgID, id.Traits["org-id"])
		signups := tracksOf(msgs, analytics.EventSignup)
		require.Len(t, signups, 1)
		assert.Equal(t, ctx.OrgID, signups[0].Properties["org-id"])
		assert.Equal(t, id.UserId, signups[0].UserId)
	})

	t.Run("an invited teammate's first sign-in is a login, with no email", func(t *testing.T) {
		rec := &recordedAnalytics{}
		signInWith(t, rec, idptypes.ProviderUserInfo{Subject: "idp|ana", Email: "ana@acme.test", Profile: "Ana"})
		msgs := rec.waitFor(t, 1)

		assert.Nil(t, identifyOf(msgs))
		assert.Empty(t, tracksOf(msgs, analytics.EventSignup), "the invite already counted this teammate")
		logins := tracksOf(msgs, analytics.EventLogin)
		require.Len(t, logins, 1)
		assert.Equal(t, anonOrg, logins[0].Properties["org-id"])
		assert.NotContains(t, logins[0].Properties, "email")
	})
}
