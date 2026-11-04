package loginoidcapi

import (
	"testing"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
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
