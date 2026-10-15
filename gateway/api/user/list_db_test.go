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
	"github.com/hoophq/hoop/common/proto"
	loginlocalapi "github.com/hoophq/hoop/gateway/api/login/local"
	userapi "github.com/hoophq/hoop/gateway/api/user"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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

// callList runs GET /users as an admin of the test org.
func callList(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	return callListAs(t, listTestOrgID, []string{types.GroupAdmin}, query)
}

func callListAs(t *testing.T, orgID string, groups []string, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("caller@hoop.dev", orgID).
		WithUserInfo("Caller", "caller@hoop.dev", "active", "", groups))
	c.Request = httptest.NewRequest(http.MethodGet, "/api/users?"+query, nil)
	userapi.List(c)
	return rec
}

// listKeys returns the sorted keys of each listed user.
func listKeys(t *testing.T, rec *httptest.ResponseRecorder) [][]string {
	t.Helper()
	var raw []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw), "GET /users must stay a JSON array")
	keys := make([][]string, len(raw))
	for i, item := range raw {
		for k := range item {
			keys[i] = append(keys[i], k)
		}
		sort.Strings(keys[i])
	}
	return keys
}

func lastLoginOf(t *testing.T, rec *httptest.ResponseRecorder, email string) *time.Time {
	t.Helper()
	var users []listedUser
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
	for _, u := range users {
		if u.Email == email {
			return u.LastLoginAt
		}
	}
	t.Fatalf("user %s is not listed", email)
	return nil
}

// callLocalAuth runs a local auth handler with an email and password body.
func callLocalAuth(t *testing.T, handler gin.HandlerFunc, path, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"email":"` + email + `","password":"` + password + `","name":"` + email + `"}`
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func signIn(t *testing.T, email, password string) {
	t.Helper()
	rec := callLocalAuth(t, loginlocalapi.Login, "/api/localauth/login", email, password)
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

		keys := listKeys(t, rec)
		require.Len(t, keys, 3)
		for _, k := range keys {
			assert.Equal(t, []string{"email", "groups", "id", "last_login_at", "name", "picture", "role", "slack_id", "status", "verified"}, k)
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

	t.Run("an auditor gets last_login_at and other roles do not", func(t *testing.T) {
		rec := callListAs(t, listTestOrgID, []string{types.GroupAuditor}, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, listKeys(t, rec)[0], "last_login_at")

		for _, groups := range [][]string{nil, {"sre"}, {types.GroupApprover}} {
			rec := callListAs(t, listTestOrgID, groups, "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			for _, k := range listKeys(t, rec) {
				assert.Equal(t, []string{"email", "groups", "id", "name", "picture", "role", "slack_id", "status", "verified"}, k,
					"groups %v must get the user fields only", groups)
			}

			rec = callListAs(t, listTestOrgID, groups, "page=1")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), "last_login_at", "groups %v", groups)
		}
	})

	t.Run("invalid params return 422", func(t *testing.T) {
		for _, query := range []string{"page_size=101", "page_size=0", "page=0", "page=2147483648", "status=deleted"} {
			rec := callList(t, query)
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "query %q: %s", query, rec.Body.String())
		}
	})

	t.Run("a local sign-in sets last_login_at", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Second)
		signIn(t, "alice@hoop.dev", "alice-pass")

		rec := callList(t, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		lastLogin := lastLoginOf(t, rec, "alice@hoop.dev")
		require.NotNil(t, lastLogin)
		assert.False(t, lastLogin.Before(before), "last login %v is before the sign-in %v", lastLogin, before)
		assert.Nil(t, lastLoginOf(t, rec, "carol@hoop.dev"), "carol never signed in")
	})

	t.Run("an inactive user's sign-in is not recorded", func(t *testing.T) {
		// Local auth hands an inactive user a token; every request with it is then refused.
		signIn(t, "bob@hoop.dev", "bob-pass")

		rec := callList(t, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Nil(t, lastLoginOf(t, rec, "bob@hoop.dev"))
	})

	t.Run("the first local register sets last_login_at", func(t *testing.T) {
		// Register signs up the first user of the default org.
		const defaultOrgID = "00000000-0000-0000-0000-0000000000c2"
		require.NoError(t, models.DB.Exec(
			`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, defaultOrgID, proto.DefaultOrgName).Error)
		before := time.Now().UTC().Add(-time.Second)
		rec := callLocalAuth(t, loginlocalapi.Register, "/api/localauth/register", "first@hoop.dev", "first-pass")
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		rec = callListAs(t, defaultOrgID, []string{types.GroupAdmin}, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		lastLogin := lastLoginOf(t, rec, "first@hoop.dev")
		require.NotNil(t, lastLogin)
		assert.False(t, lastLogin.Before(before), "last login %v is before the register %v", lastLogin, before)
	})
}
