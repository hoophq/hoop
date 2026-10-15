package loginoidcapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const callbackTestOrgID = "00000000-0000-0000-0000-0000000000e1"

// fakeVerifier stands in for the identity provider: every code and access
// token belongs to uinfo.
type fakeVerifier struct{ uinfo idptypes.ProviderUserInfo }

func (f fakeVerifier) VerifyAccessToken(string) (string, error)             { return f.uinfo.Subject, nil }
func (fakeVerifier) GetAudience() string                                    { return "" }
func (fakeVerifier) GetAuthCodeURL(string, ...oauth2.AuthCodeOption) string { return "" }

func (f fakeVerifier) VerifyIDTokenForCode(string) (*oauth2.Token, idptypes.ProviderUserInfo, error) {
	return &oauth2.Token{AccessToken: "access-" + f.uinfo.Subject}, f.uinfo, nil
}

func (f fakeVerifier) VerifyAccessTokenWithUserInfo(string) (*idptypes.ProviderUserInfo, error) {
	uinfo := f.uinfo
	return &uinfo, nil
}

// signIn runs the OIDC callback for the identity the provider returns and
// gives back the redirect location.
func signIn(t *testing.T, uinfo idptypes.ProviderUserInfo) string {
	t.Helper()
	state := uuid.NewString()
	const redirect = "http://localhost:8009/callback"
	require.NoError(t, models.CreateLogin(&models.Login{ID: state, Redirect: redirect}))

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/callback?code=the-code&state="+state, nil)
	h := &handler{newVerifier: func() (idp.OidcVerifier, error) { return fakeVerifier{uinfo: uinfo}, nil }}
	h.LoginCallback(c)
	require.Equal(t, http.StatusTemporaryRedirect, rec.Code, rec.Body.String())
	return rec.Header().Get("Location")
}

func seedCallbackUser(t *testing.T, subject, email, status string) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`
		INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, ?)`, callbackTestOrgID, subject, email, email, status).Error)
}

// userRow reads the stored subject, status and last login of the user with this email.
func userRow(t *testing.T, email string) (subject, status string, lastLogin *time.Time) {
	t.Helper()
	var at sql.NullTime
	row := models.DB.Raw(`SELECT subject, status, last_login_at FROM private.users WHERE email = ?`, email).Row()
	require.NoError(t, row.Scan(&subject, &status, &at))
	if at.Valid {
		lastLogin = &at.Time
	}
	return subject, status, lastLogin
}

// The callback records the login only after the user sync made the subject
// final, and never for a refused sign-in.
func TestLoginCallbackRecordsLastLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'oidc-callback-test')`, callbackTestOrgID).Error)

	t.Run("an invited user's first sign-in is recorded under the provider subject", func(t *testing.T) {
		// An admin invites by email: the stored subject is the email until the first sign-in.
		seedCallbackUser(t, "bruna@hoop.dev", "bruna@hoop.dev", "invited")
		before := time.Now().UTC().Add(-time.Second)

		location := signIn(t, idptypes.ProviderUserInfo{Subject: "idp|bruna", Email: "bruna@hoop.dev", Profile: "Bruna"})
		assert.NotContains(t, location, "error=")

		subject, status, lastLogin := userRow(t, "bruna@hoop.dev")
		assert.Equal(t, "idp|bruna", subject)
		assert.Equal(t, "active", status)
		require.NotNil(t, lastLogin)
		assert.False(t, lastLogin.Before(before), "last login %v is before the sign-in %v", *lastLogin, before)

		// The refresh, the session poller and the native proxies look the token
		// up by the provider subject, so the first session must find it there.
		userToken, err := models.GetUserToken(models.DB, "idp|bruna")
		require.NoError(t, err)
		require.NotNil(t, userToken, "the first sign-in must store the token under the provider subject")
		assert.Equal(t, "access-idp|bruna", userToken.Token)
		orphan, err := models.GetUserToken(models.DB, "bruna@hoop.dev")
		require.NoError(t, err)
		assert.Nil(t, orphan, "no token is stored under the invited email")
	})

	t.Run("a returning user's sign-in is recorded", func(t *testing.T) {
		seedCallbackUser(t, "idp|dana", "dana@hoop.dev", "active")
		before := time.Now().UTC().Add(-time.Second)

		location := signIn(t, idptypes.ProviderUserInfo{Subject: "idp|dana", Email: "dana@hoop.dev", Profile: "Dana"})
		assert.NotContains(t, location, "error=")

		_, _, lastLogin := userRow(t, "dana@hoop.dev")
		require.NotNil(t, lastLogin)
		assert.False(t, lastLogin.Before(before), "last login %v is before the sign-in %v", *lastLogin, before)
	})

	t.Run("an inactive user's refused sign-in is not recorded", func(t *testing.T) {
		seedCallbackUser(t, "idp|carl", "carl@hoop.dev", "inactive")

		location := signIn(t, idptypes.ProviderUserInfo{Subject: "idp|carl", Email: "carl@hoop.dev", Profile: "Carl"})
		assert.True(t, strings.Contains(location, "error="), "the sign-in must be refused, got %q", location)

		_, status, lastLogin := userRow(t, "carl@hoop.dev")
		assert.Equal(t, "inactive", status)
		assert.Nil(t, lastLogin)
	})
}
