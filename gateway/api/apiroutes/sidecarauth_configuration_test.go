package apiroutes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A route behind SidecarAuthWithoutConfigurationMiddleware gets the sidecar
// with an empty configuration, and SidecarConfigurationFromContext answers
// nil there: a handler that needs the configuration and was mounted on the
// wrong middleware refuses instead of acting on an empty one. Both carry the
// organization's license, read with the sidecar row.
func TestSidecarAuthWithAndWithoutConfiguration(t *testing.T) {
	startSidecarAuthDB(t)
	require.NoError(t, models.DB.Exec(`UPDATE private.orgs SET license_data = '{"payload":{"type":"oss"}}' WHERE id = ?`,
		sidecarOrgA).Error)
	const token = "hsc_configuration_token"
	require.NoError(t, models.CreateSidecar(models.DB, &models.Sidecar{
		OrgID: sidecarOrgA, Name: "edge", KeyHash: models.HashAPIKey(token), CreatedBy: "admin@hoop.dev",
		Configuration: models.SidecarConfiguration(daemon.Config{Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"}}}),
	}))
	r := &Router{apiURL: "http://localhost:8009"}

	type seen struct {
		any, withConfiguration *models.Sidecar
		license                string
		deadline               bool
	}
	call := func(middleware gin.HandlerFunc) seen {
		gin.SetMode(gin.TestMode)
		var got seen
		engine := gin.New()
		engine.POST("/api/sidecars/x", middleware, func(c *gin.Context) {
			got.any = SidecarFromContext(c)
			got.withConfiguration = SidecarConfigurationFromContext(c)
			_, got.deadline = c.Request.Context().Deadline()
			if l := storagev2.ParseContext(c).OrgLicenseData; l != nil {
				got.license = string(*l)
			}
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodPost, "/api/sidecars/x", nil)
		req.Header.Set(SidecarTokenHeader, token)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		return got
	}

	full := call(r.SidecarAuthMiddleware)
	require.NotNil(t, full.withConfiguration)
	assert.Len(t, full.withConfiguration.Configuration.Listeners, 1)
	assert.NotEmpty(t, full.withConfiguration.ConfigurationHash)
	assert.JSONEq(t, `{"payload":{"type":"oss"}}`, full.license, "the license read with the row")
	assert.True(t, full.deadline, "the request runs under SidecarRequestTimeout")

	light := call(r.SidecarAuthWithoutConfigurationMiddleware)
	require.NotNil(t, light.any)
	assert.Equal(t, full.any.ID, light.any.ID)
	assert.Empty(t, light.any.Configuration.Listeners, "the configuration is not read")
	assert.Nil(t, light.withConfiguration, "a handler that needs the configuration refuses")
	assert.JSONEq(t, `{"payload":{"type":"oss"}}`, light.license)
	assert.True(t, light.deadline)
}
