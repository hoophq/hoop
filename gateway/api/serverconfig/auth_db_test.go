package apiserverconfig_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const testOrgID = "00000000-0000-0000-0000-0000000000a5"

func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'auth-config-test')`, testOrgID).Error)
}

// newIssuer serves the discovery document and the JWKS the save pre-flight reads.
func newIssuer(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// call runs handler as an admin of the test org.
func call(t *testing.T, handler gin.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin@hoop.dev", testOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(method, "/api/serverconfig/auth", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func putOidc(t *testing.T, oidcConfig map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"auth_method":                    "oidc",
		"webapp_users_management_status": "inactive",
		"oidc_config":                    oidcConfig,
	})
	require.NoError(t, err)
	return call(t, apiserverconfig.UpdateAuthConfig, http.MethodPut, string(body))
}

func storedOidcConfig(t *testing.T) map[string]any {
	t.Helper()
	var raw string
	require.NoError(t, models.DB.Raw(`SELECT oidc_config::text FROM private.authconfig WHERE org_id = ?`, testOrgID).Scan(&raw).Error)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
	return cfg
}

func TestUpdateAuthConfigClientAssertionFile(t *testing.T) {
	startTestDB(t)
	issuer := newIssuer(t)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("eyJhbGciOiJSUzI1NiJ9.e30.sig\n"), 0o600))
	emptyFile := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(emptyFile, nil, 0o600))
	notJWT := filepath.Join(dir, "environ")
	require.NoError(t, os.WriteFile(notJWT, []byte("POSTGRES_DB_URI=postgres://u:p@db/hoop\n"), 0o600))

	t.Run("a client secret is stored as before, with no assertion key", func(t *testing.T) {
		rec := putOidc(t, map[string]any{"issuer_url": issuer, "client_id": "cid", "client_secret": "s3cret"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		cfg := storedOidcConfig(t)
		assert.Equal(t, "s3cret", cfg["client_secret"])
		assert.NotContains(t, cfg, "client_assertion_file")
	})

	t.Run("the client assertion file the operator set is stored and returned", func(t *testing.T) {
		t.Setenv("IDP_CLIENT_ASSERTION_FILE", tokenFile)
		rec := putOidc(t, map[string]any{"issuer_url": issuer, "client_id": "cid", "client_assertion_file": tokenFile})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, tokenFile, storedOidcConfig(t)["client_assertion_file"])

		rec = call(t, apiserverconfig.GetAuthConfig, http.MethodGet, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp openapi.ServerAuthConfig
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.NotNil(t, resp.OidcConfig)
		assert.Equal(t, tokenFile, resp.OidcConfig.ClientAssertionFile)
		assert.Empty(t, resp.OidcConfig.ClientSecret)
	})

	for _, tt := range []struct {
		name     string
		operator string
		config   map[string]any
		wantErr  string
	}{
		{"both", tokenFile, map[string]any{"client_secret": "s3cret", "client_assertion_file": tokenFile}, "not both"},
		{"neither", tokenFile, map[string]any{}, "client-secret or client-assertion-file"},
		{"a path the operator did not set", tokenFile, map[string]any{"client_assertion_file": notJWT}, "must be " + tokenFile},
		{"a path when the operator set none", "", map[string]any{"client_assertion_file": tokenFile}, "did not allow a client assertion file"},
		{"an empty file", emptyFile, map[string]any{"client_assertion_file": emptyFile}, "is empty"},
		{"a missing file", filepath.Join(dir, "missing"), map[string]any{"client_assertion_file": filepath.Join(dir, "missing")}, "failed reading client assertion file"},
		{"a file that is not a JWT", notJWT, map[string]any{"client_assertion_file": notJWT}, "does not hold a JWT"},
	} {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Setenv("IDP_CLIENT_ASSERTION_FILE", tt.operator)
			tt.config["issuer_url"] = issuer
			tt.config["client_id"] = "cid"
			rec := putOidc(t, tt.config)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tt.wantErr)
		})
	}
}
