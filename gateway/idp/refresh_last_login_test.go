package idp

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

// refreshingVerifier refreshes every token. It has no userinfo endpoint, so
// the refresh does not sync groups.
type refreshingVerifier struct{}

func (refreshingVerifier) VerifyAccessToken(string) (string, error) { return "", nil }

func (refreshingVerifier) RefreshAccessToken(context.Context, string) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "refreshed-access-token"}, nil
}

func TestRefreshRecordsLastLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	const orgID = "00000000-0000-0000-0000-0000000000d1"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'idp-refresh-test')`, orgID).Error)

	lastLogin := func(subject string) *time.Time {
		var at sql.NullTime
		require.NoError(t, models.DB.Raw(
			`SELECT last_login_at FROM private.users WHERE subject = ?`, subject).Row().Scan(&at))
		if !at.Valid {
			return nil
		}
		return &at.Time
	}

	for _, tc := range []struct {
		subject, status string
		recorded        bool
	}{
		{"idp|active", "active", true},
		{"idp|inactive", "inactive", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			require.NoError(t, models.DB.Exec(`
				INSERT INTO private.users (org_id, subject, email, name, status)
				VALUES (?, ?, ?, ?, ?)`, orgID, tc.subject, tc.subject+"@hoop.dev", tc.subject, tc.status).Error)
			refreshToken := "refresh-" + tc.subject
			require.NoError(t, models.UpsertUserToken(models.DB, tc.subject, "old-access-token", &refreshToken))

			before := time.Now().UTC().Add(-time.Second)
			_, err := TryProactiveRefreshForVerifiedSubject(refreshingVerifier{}, tc.subject)
			require.NoError(t, err)

			at := lastLogin(tc.subject)
			if !tc.recorded {
				assert.Nil(t, at, "a refresh of an inactive user is not a login")
				return
			}
			require.NotNil(t, at)
			assert.False(t, at.Before(before), "last login %v is before the refresh %v", *at, before)
		})
	}
}
