package apiroutes

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/gateway/externaljwt"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sidecarOrgA     = "00000000-0000-0000-0000-0000000000c1"
	sidecarOrgB     = "00000000-0000-0000-0000-0000000000c2"
	sidecarAudience = "https://hoop.example.com"
)

// callSidecarAuth runs the middleware in front of a handler that reports the
// sidecar and organization it was handed.
func callSidecarAuth(r *Router, headers map[string]string) (*httptest.ResponseRecorder, *models.Sidecar, string) {
	gin.SetMode(gin.TestMode)
	var (
		reached *models.Sidecar
		orgID   string
	)
	engine := gin.New()
	engine.POST("/api/sidecars/handshake", r.SidecarAuthMiddleware, func(c *gin.Context) {
		reached = SidecarFromContext(c)
		orgID = storagev2.ParseContext(c).OrgID
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w, reached, orgID
}

// noNetwork fails the test on any request.
func noNetwork(t *testing.T) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected fetch of %s", r.URL)
		return nil, errors.New("no network in this test")
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Two credentials name two sidecars, or one twice: either way the request
// cannot say which sidecar is calling.
func TestSidecarAuthRefusesBothCredentials(t *testing.T) {
	r := &Router{sidecarIdentity: externaljwt.NewOIDCVerifier(externaljwt.OIDCOptions{HTTPClient: noNetwork(t)})}
	w, reached, _ := callSidecarAuth(r, map[string]string{
		SidecarTokenHeader:    "hsc_token",
		SidecarIdentityHeader: "a.b.c",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Nil(t, reached, "the handler ran")
	assert.Contains(t, w.Body.String(), "not both")
}

func TestSidecarAuthRefusesAnIdentityThatIsNotAJWT(t *testing.T) {
	r := &Router{sidecarIdentity: externaljwt.NewOIDCVerifier(externaljwt.OIDCOptions{HTTPClient: noNetwork(t)})}
	w, reached, _ := callSidecarAuth(r, map[string]string{SidecarIdentityHeader: "hsc_not-a-jwt"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Nil(t, reached)
}

type testIssuer struct {
	*httptest.Server
	key *rsa.PrivateKey
}

// newTestIssuer serves OIDC discovery and one RSA key over TLS.
func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	iss := &testIssuer{key: key}
	iss.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss.URL, "jwks_uri": iss.URL + "/keys"})
		case "/keys":
			enc := base64.RawURLEncoding
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": enc.EncodeToString(key.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(iss.Close)
	return iss
}

func (iss *testIssuer) token(t *testing.T, issuer, audience, subject string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": subject,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(iss.key)
	require.NoError(t, err)
	return s
}

func startSidecarAuthDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	for _, org := range []string{sidecarOrgA, sidecarOrgB} {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, org, "org-"+org[len(org)-2:]).Error)
	}
}

func TestSidecarIdentityAuth(t *testing.T) {
	startSidecarAuthDB(t)
	iss := newTestIssuer(t)
	r := &Router{apiURL: "http://localhost:8009",
		sidecarIdentity: externaljwt.NewOIDCVerifier(externaljwt.OIDCOptions{HTTPClient: iss.Client()})}
	require.NoError(t, models.CreateSidecarServiceAccount(models.DB, &models.SidecarServiceAccount{
		OrgID: sidecarOrgA, Name: "gke-eu", Issuer: iss.URL, Audience: sidecarAudience, Claim: services.SidecarClaimSub,
		SubjectPattern: "system:serviceaccount:*:hoop-sidecar", NameTemplate: "gke-eu-{1}", CreatedBy: "admin@hoop.dev",
	}))
	identity := func(subject string) map[string]string {
		return map[string]string{SidecarIdentityHeader: iss.token(t, iss.URL, sidecarAudience, subject)}
	}
	refused := func(t *testing.T, headers map[string]string) string {
		t.Helper()
		w, reached, _ := callSidecarAuth(r, headers)
		require.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body)
		require.Nil(t, reached, "the handler ran")
		var body struct{ Message string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body.Message
	}

	t.Run("the first boot creates the sidecar and later boots reuse it", func(t *testing.T) {
		w, first, orgID := callSidecarAuth(r, identity("system:serviceaccount:ws-1:hoop-sidecar"))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		require.NotNil(t, first)
		assert.Equal(t, "gke-eu-ws-1", first.Name)
		assert.Equal(t, sidecarOrgA, first.OrgID)
		assert.Equal(t, sidecarOrgA, orgID, "the org context is the sidecar's")

		// A restart, another pod or a rotated token: the same name, the same row.
		w, again, _ := callSidecarAuth(r, identity("system:serviceaccount:ws-1:hoop-sidecar"))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, first.ID, again.ID)
		items, err := models.ListSidecars(models.DB, sidecarOrgA)
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("a sidecar an admin made with a token is reached and its token still works", func(t *testing.T) {
		const token = "hsc_ws2_token"
		made := &models.Sidecar{OrgID: sidecarOrgA, Name: "gke-eu-ws-2", KeyHash: models.HashAPIKey(token), CreatedBy: "admin@hoop.dev"}
		require.NoError(t, models.CreateSidecar(models.DB, made))

		w, reached, _ := callSidecarAuth(r, identity("system:serviceaccount:ws-2:hoop-sidecar"))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, made.ID, reached.ID)

		w, reached, _ = callSidecarAuth(r, map[string]string{SidecarTokenHeader: token})
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, made.ID, reached.ID)
	})

	t.Run("a deleted name is refused and nothing is created", func(t *testing.T) {
		require.NoError(t, models.InsertSidecarDeletedName(models.DB, sidecarOrgA, "gke-eu-ws-3", "admin@hoop.dev"))
		msg := refused(t, identity("system:serviceaccount:ws-3:hoop-sidecar"))
		assert.Equal(t, services.SidecarDeletedMessage, msg)
		_, err := models.GetSidecarByNameOrID(models.DB, sidecarOrgA, "gke-eu-ws-3")
		assert.ErrorIs(t, err, models.ErrNotFound)
	})

	t.Run("a wrong audience is refused", func(t *testing.T) {
		refused(t, map[string]string{SidecarIdentityHeader: iss.token(t, iss.URL, "https://other.example.com", "system:serviceaccount:ws-4:hoop-sidecar")})
	})

	t.Run("a subject no mapping allows is refused", func(t *testing.T) {
		msg := refused(t, identity("system:serviceaccount:ws-5:default"))
		assert.Contains(t, msg, "system:serviceaccount:ws-5:default")
	})

	t.Run("an unknown issuer is refused without a fetch", func(t *testing.T) {
		offline := &Router{apiURL: r.apiURL,
			sidecarIdentity: externaljwt.NewOIDCVerifier(externaljwt.OIDCOptions{HTTPClient: noNetwork(t)})}
		w, reached, _ := callSidecarAuth(offline, map[string]string{
			SidecarIdentityHeader: iss.token(t, "https://unknown.example.com", sidecarAudience, "system:serviceaccount:ws-1:hoop-sidecar"),
		})
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, reached)
	})

	t.Run("a match in two organizations is refused", func(t *testing.T) {
		// The write path refuses a second organization on the same issuer
		// and audience; the runtime refusal still holds for rows that reach
		// the table another way, such as rows written before that check.
		err := models.CreateSidecarServiceAccount(models.DB, &models.SidecarServiceAccount{
			OrgID: sidecarOrgB, Name: "gke-eu", Issuer: iss.URL, Audience: sidecarAudience, Claim: services.SidecarClaimSub,
			SubjectPattern: "system:serviceaccount:ws-9:hoop-sidecar", NameTemplate: "ws-9", CreatedBy: "admin@hoop.dev",
		})
		require.ErrorIs(t, err, models.ErrSidecarServiceAccountPairTaken)
		require.NoError(t, models.DB.Exec(`
			INSERT INTO private.sidecar_service_accounts (org_id, name, issuer, audience, claim,
				subject_pattern, name_template, created_by)
			VALUES (?, 'gke-eu', ?, ?, 'sub', 'system:serviceaccount:ws-9:hoop-sidecar', 'ws-9', 'admin@hoop.dev')`,
			sidecarOrgB, iss.URL, sidecarAudience).Error)
		refused(t, identity("system:serviceaccount:ws-9:hoop-sidecar"))
		for _, org := range []string{sidecarOrgA, sidecarOrgB} {
			items, err := models.ListSidecars(models.DB, org)
			require.NoError(t, err)
			for _, sc := range items {
				assert.NotContains(t, sc.Name, "ws-9", "an ambiguous token created a sidecar in %s", org)
			}
		}
	})
}
