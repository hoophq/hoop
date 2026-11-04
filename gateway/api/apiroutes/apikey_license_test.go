package apiroutes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under env-only auth private.authconfig has no row, so the server config
// carries no license. The legacy Api-Key path must still hand
// EnterpriseLicenseOnly the document stored on the organization.
func TestLegacyApiKeyCarriesOrgLicense(t *testing.T) {
	startSidecarAuthDB(t)
	apiKey := sidecarOrgA + "|secret"
	t.Setenv("API_KEY", apiKey)
	t.Setenv("AUTH_METHOD", "local")
	require.NoError(t, appconfig.Load())

	doc := `{"payload":{"type":"enterprise","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`
	require.NoError(t, models.UpdateOrgLicense(sidecarOrgA, []byte(doc)))

	gin.SetMode(gin.TestMode)
	r := &Router{apiURL: "http://localhost:8009"}
	var got *json.RawMessage
	engine := gin.New()
	engine.GET("/api/sidecar-service-accounts", r.AuthMiddleware, func(c *gin.Context) {
		got = storagev2.ParseContext(c).OrgLicenseData
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar-service-accounts", nil)
	req.Header.Set("Api-Key", apiKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	require.NotNil(t, got, "the context carries no license")
	assert.JSONEq(t, doc, string(*got))
}
