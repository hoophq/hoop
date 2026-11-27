package loginoidcapi

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/common/proto"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A user is found by email. Two people with no email would share one row,
// and the second sign-in would take the first one's account.
func TestLoginCallbackRefusesAnIdentityWithNoEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	// The default org is where a single-tenant gateway creates a new user.
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, callbackTestOrgID, proto.DefaultOrgName).Error)

	for _, subject := range []string{"pf|alice", "pf|bob"} {
		location := signIn(t, idptypes.ProviderUserInfo{Subject: subject})
		assert.True(t, strings.Contains(location, "error="), "the sign-in of %s must be refused, got %q", subject, location)
	}

	var users int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.users`).Scan(&users).Error)
	assert.Zero(t, users, "a refused sign-in creates no user")
}
