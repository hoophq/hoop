package loginoidcapi

import (
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/analytics"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	segment "github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	t.Run("an unknown subject keeps the context it was given", func(t *testing.T) {
		stale := &models.Context{UserEmail: "kept@acme.test"}
		ctx := reloadSignupContext(stale, "idp|nobody")
		assert.Same(t, stale, ctx)
	})
}

// recordedAnalytics keeps what a sign-in hands the analytics client, in order.
type recordedAnalytics struct {
	mu     sync.Mutex
	msgs   []segment.Message
	closed chan []segment.Message
}

func newRecordedAnalytics() *recordedAnalytics {
	return &recordedAnalytics{closed: make(chan []segment.Message, 1)}
}

func (r *recordedAnalytics) Enqueue(m segment.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return nil
}

// Close hands over what was enqueued before it: anything enqueued after a
// real client is closed is dropped.
func (r *recordedAnalytics) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed <- append([]segment.Message(nil), r.msgs...)
	return nil
}

// track runs the sign-in analytics and returns what reached the client before
// it was closed.
func track(t *testing.T, isNewUser bool, ctx *models.Context) []segment.Message {
	t.Helper()
	rec := newRecordedAnalytics()
	h := &handler{newAnalytics: func() *analytics.Segment { return &analytics.Segment{Client: rec} }}
	h.analyticsTrack(isNewUser, "test-agent", ctx)
	select {
	case msgs := <-rec.closed:
		return msgs
	case <-time.After(5 * time.Second):
		t.Fatal("the analytics client was never closed")
		return nil
	}
}

func identifyOf(msgs []segment.Message) *segment.Identify {
	for _, m := range msgs {
		if id, ok := m.(segment.Identify); ok {
			return &id
		}
	}
	return nil
}

func trackOf(msgs []segment.Message, event string) *segment.Track {
	for _, m := range msgs {
		if tr, ok := m.(segment.Track); ok && tr.Event == event {
			return &tr
		}
	}
	return nil
}

// A new multi-tenant sign-up reaches analytics as one person: the Identify
// carries the email only when the org's mode allows it, and the hoop-signup
// event is enqueued before the client is closed.
func TestAnalyticsTrackSignup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	delay := signupTrackDelay
	signupTrackDelay = 0
	t.Cleanup(func() { signupTrackDelay = delay })

	isNewUser, err := registerMultiTenantUser(idptypes.ProviderUserInfo{
		Subject: "idp|fay", Email: "fay@acme.test", Profile: "Fay"}, "")
	require.NoError(t, err)
	require.True(t, isNewUser)
	ctx := reloadSignupContext(&models.Context{}, "idp|fay")
	require.NotEmpty(t, ctx.OrgID)

	t.Run("identified mode: the email goes out and hoop-signup follows", func(t *testing.T) {
		msgs := track(t, true, ctx)

		id := identifyOf(msgs)
		require.NotNil(t, id, "the sign-up must be identified")
		assert.Equal(t, "fay@acme.test", id.Traits["email"])
		assert.Equal(t, ctx.OrgID, id.Traits["org-id"])

		signup := trackOf(msgs, analytics.EventSignup)
		require.NotNil(t, signup, "hoop-signup must be enqueued before the client is closed")
		assert.Equal(t, ctx.OrgID, signup.Properties["org-id"])
		assert.Equal(t, id.UserId, signup.UserId, "the event and the person must share one id")
	})

	t.Run("anonymous mode: hashed id only, no email", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeAnonymous))
		anonCtx := reloadSignupContext(ctx, "idp|fay")
		require.Equal(t, models.AnalyticsModeAnonymous, anonCtx.OrgAnalyticsMode)

		msgs := track(t, true, anonCtx)

		id := identifyOf(msgs)
		require.NotNil(t, id)
		assert.NotContains(t, id.Traits, "email")
		assert.NotContains(t, id.Traits, "name")
		assert.NotNil(t, trackOf(msgs, analytics.EventSignup))
	})

	t.Run("disabled mode: nothing goes out", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeDisabled))
		offCtx := reloadSignupContext(ctx, "idp|fay")
		require.Equal(t, models.AnalyticsModeDisabled, offCtx.OrgAnalyticsMode)

		assert.Empty(t, track(t, true, offCtx))
	})

	t.Run("a returning user's hoop-login still goes out", func(t *testing.T) {
		require.NoError(t, models.UpdateOrgAnalyticsMode(ctx.OrgID, models.AnalyticsModeIdentified))
		loginCtx := reloadSignupContext(ctx, "idp|fay")
		analytics.SetMode(loginCtx.OrgID, loginCtx.OrgAnalyticsMode)

		msgs := track(t, false, loginCtx)
		assert.Nil(t, identifyOf(msgs))
		assert.NotNil(t, trackOf(msgs, analytics.EventLogin))
	})
}
