package models_test

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedListUser creates a user with the given name, email and status. The
// subject is the email, as local auth stores it.
func seedListUser(t *testing.T, name, email, status string) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`
		INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, ?)`, testOrgID, email, email, name, status).Error)
}

func listEmails(users []models.UserWithLastLogin) []string {
	emails := make([]string, len(users))
	for i, u := range users {
		emails[i] = u.Email
	}
	return emails
}

func TestListUsersWithLastLogin(t *testing.T) {
	startTestDB(t)
	seedListUser(t, "carol", "carol@hoop.dev", "active")
	seedListUser(t, "Alice", "alice@hoop.dev", "active")
	seedListUser(t, "bob", "bob@hoop.dev", "inactive")
	seedListUser(t, "dave", "da_ve@hoop.dev", "invited")
	seedListUser(t, "erin", "daxve@hoop.dev", "active")

	before := time.Now().UTC().Add(-time.Second)
	require.NoError(t, models.RecordUserLogin(models.DB, "alice@hoop.dev"))
	// Refused sign-ins: only an active user gets a last login.
	require.NoError(t, models.RecordUserLogin(models.DB, "bob@hoop.dev"))
	require.NoError(t, models.RecordUserLogin(models.DB, "da_ve@hoop.dev"))

	t.Run("without a page it keeps the ListUsers order and returns the last login", func(t *testing.T) {
		users, total, err := models.ListUsersWithLastLogin(models.DB, testOrgID, models.UserListOption{})
		require.NoError(t, err)
		assert.EqualValues(t, 5, total)
		assert.Equal(t, []string{"daxve@hoop.dev", "da_ve@hoop.dev", "carol@hoop.dev", "bob@hoop.dev", "alice@hoop.dev"}, listEmails(users))
		for _, u := range users {
			if u.Email == "alice@hoop.dev" {
				require.NotNil(t, u.LastLoginAt)
				assert.False(t, u.LastLoginAt.Before(before), "last login %v is before the sign-in %v", u.LastLoginAt, before)
				continue
			}
			assert.Nil(t, u.LastLoginAt, "user %s never signed in", u.Email)
		}
	})

	t.Run("email matches a part, ignoring case", func(t *testing.T) {
		users, total, err := models.ListUsersWithLastLogin(models.DB, testOrgID, models.UserListOption{Email: "ALICE"})
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		assert.Equal(t, []string{"alice@hoop.dev"}, listEmails(users))
	})

	t.Run("email treats LIKE wildcards as text", func(t *testing.T) {
		users, _, err := models.ListUsersWithLastLogin(models.DB, testOrgID, models.UserListOption{Email: "da_ve"})
		require.NoError(t, err)
		assert.Equal(t, []string{"da_ve@hoop.dev"}, listEmails(users))
	})

	t.Run("status matches exactly", func(t *testing.T) {
		users, total, err := models.ListUsersWithLastLogin(models.DB, testOrgID, models.UserListOption{Status: "inactive"})
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		assert.Equal(t, []string{"bob@hoop.dev"}, listEmails(users))
	})

	t.Run("pages sort by name, do not overlap and count every match", func(t *testing.T) {
		var seen []string
		for page := 1; page <= 3; page++ {
			users, total, err := models.ListUsersWithLastLogin(models.DB, testOrgID,
				models.UserListOption{Page: page, PageSize: 2})
			require.NoError(t, err)
			assert.EqualValues(t, 5, total)
			seen = append(seen, listEmails(users)...)
		}
		assert.Equal(t, []string{"alice@hoop.dev", "bob@hoop.dev", "carol@hoop.dev", "da_ve@hoop.dev", "daxve@hoop.dev"}, seen)
	})

	t.Run("a page past the end is empty but keeps the total", func(t *testing.T) {
		users, total, err := models.ListUsersWithLastLogin(models.DB, testOrgID,
			models.UserListOption{Status: "active", Page: 9, PageSize: 2})
		require.NoError(t, err)
		assert.Empty(t, users)
		assert.EqualValues(t, 3, total)
	})

	t.Run("a user created again with the same email starts without a last login", func(t *testing.T) {
		require.NoError(t, models.DB.Exec(
			`DELETE FROM private.users WHERE org_id = ? AND subject = 'alice@hoop.dev'`, testOrgID).Error)
		seedListUser(t, "Alice", "alice@hoop.dev", "active")
		users, _, err := models.ListUsersWithLastLogin(models.DB, testOrgID, models.UserListOption{Email: "alice"})
		require.NoError(t, err)
		require.Len(t, users, 1)
		assert.Nil(t, users[0].LastLoginAt)
	})
}
