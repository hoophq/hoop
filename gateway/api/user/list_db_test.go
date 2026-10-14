package userapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	loginlocalapi "github.com/hoophq/hoop/gateway/api/login/local"
	userapi "github.com/hoophq/hoop/gateway/api/user"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const listTestOrgID = "00000000-0000-0000-0000-0000000000c1"

func startListTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'users-list-test')`, listTestOrgID).Error)
	// Local auth, so the sign-in below goes through the real local login handler.
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.authconfig (org_id, auth_method) VALUES (?, 'local')`, listTestOrgID).Error)
}

// seedUser creates a local auth user (subject = email) with a password and groups.
func seedUser(t *testing.T, name, email, status, password string, groups ...string) {
	t.Helper()
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	var userID string
	require.NoError(t, models.DB.Raw(`
		INSERT INTO private.users (org_id, subject, email, name, status, hashed_password)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		listTestOrgID, email, email, name, status, string(hashed)).Scan(&userID).Error)
	for _, g := range groups {
		require.NoError(t, models.DB.Exec(
			`INSERT INTO private.user_groups (org_id, user_id, name) VALUES (?, ?, ?)`,
			listTestOrgID, userID, g).Error)
	}
}

func callList(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin@hoop.dev", listTestOrgID))
	c.Request = httptest.NewRequest(http.MethodGet, "/api/users?"+query, nil)
	userapi.List(c)
	return rec
}

func signIn(t *testing.T, email, password string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"email":"` + email + `","password":"` + password + `"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/localauth/login", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	loginlocalapi.Login(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Token"))
}

type listedUser struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	Role        string     `json:"role"`
	Groups      []string   `json:"groups"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func TestListUsers(t *testing.T) {
	startListTestDB(t)
	seedUser(t, "Alice", "alice@hoop.dev", "active", "alice-pass", "admin", "sre")
	seedUser(t, "bob", "bob@hoop.dev", "inactive", "bob-pass")
	seedUser(t, "carol", "carol@hoop.dev", "invited", "carol-pass", "dba")

	t.Run("without page params it returns the plain array with the same keys plus last_login_at", func(t *testing.T) {
		rec := callList(t, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var raw []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw), "GET /users must stay a JSON array")
		require.Len(t, raw, 3)
		for _, item := range raw {
			keys := make([]string, 0, len(item))
			for k := range item {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			assert.Equal(t, []string{"email", "groups", "id", "last_login_at", "name", "picture", "role", "slack_id", "status", "verified"}, keys)
		}

		var users []listedUser
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
		// Same order as before: email descending.
		assert.Equal(t, "carol@hoop.dev", users[0].Email)
		assert.Equal(t, "bob@hoop.dev", users[1].Email)
		assert.Equal(t, "alice@hoop.dev", users[2].Email)
		// The id stays the subject; groups and role come from user_groups.
		assert.Equal(t, "alice@hoop.dev", users[2].ID)
		assert.ElementsMatch(t, []string{"admin", "sre"}, users[2].Groups)
		assert.Equal(t, "admin", users[2].Role)
		assert.Equal(t, []string{}, users[1].Groups)
		assert.Equal(t, "standard", users[1].Role)
	})

	t.Run("with page params it returns the envelope with the filtered total", func(t *testing.T) {
		rec := callList(t, "page=1&page_size=1&status=active")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			Pages struct{ Total, Page, Size int } `json:"pages"`
			Data  []listedUser                    `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, 1, out.Pages.Total)
		assert.Equal(t, 1, out.Pages.Page)
		assert.Equal(t, 1, out.Pages.Size)
		require.Len(t, out.Data, 1)
		assert.Equal(t, "alice@hoop.dev", out.Data[0].Email)

		rec = callList(t, "page=2&page_size=2")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, 3, out.Pages.Total)
		require.Len(t, out.Data, 1)
		assert.Equal(t, "carol@hoop.dev", out.Data[0].Email)
	})

	t.Run("filters work without page params", func(t *testing.T) {
		rec := callList(t, "email=BO")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var users []listedUser
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
		require.Len(t, users, 1)
		assert.Equal(t, "bob@hoop.dev", users[0].Email)
	})

	t.Run("invalid params return 422", func(t *testing.T) {
		for _, query := range []string{"page_size=101", "page_size=0", "page=0", "status=deleted"} {
			rec := callList(t, query)
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "query %q: %s", query, rec.Body.String())
		}
	})

	t.Run("a local sign-in sets last_login_at", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Second)
		signIn(t, "bob@hoop.dev", "bob-pass")

		rec := callList(t, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var users []listedUser
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
		for _, u := range users {
			if u.Email == "bob@hoop.dev" {
				require.NotNil(t, u.LastLoginAt)
				assert.False(t, u.LastLoginAt.Before(before), "last login %v is before the sign-in %v", u.LastLoginAt, before)
				continue
			}
			assert.Nil(t, u.LastLoginAt, "user %s never signed in", u.Email)
		}
	})
}
