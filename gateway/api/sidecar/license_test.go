package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// A license document, the shape private.orgs.license_data holds. The
// signature is never checked here: the gateway verified it when the admin
// posted it, and the sidecar verifies it again when it arrives.
const licenseDoc = `{"payload":{"type":"enterprise","issued_at":1,"expire_at":2,` +
	`"allowed_hosts":["*"],"description":"Acme"},"key_id":"k1","signature":"c2ln"}`

func storedConfig() models.SidecarConfiguration {
	return models.SidecarConfiguration{
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "h:5432"},
		},
	}
}

// The whole point: the sidecar is licensed by the organization it belongs
// to, without the license being stored per sidecar.
func TestTheServedConfigCarriesTheOrganizationLicense(t *testing.T) {
	served := servedConfig(storedConfig(), json.RawMessage(licenseDoc))

	assert.Equal(t, licenseDoc, served.License,
		"the sidecar reads the license from this key; empty means it never sees one")
}

// No license on the organization leaves the key out, so the sidecar falls
// back to its own sources instead of loading an empty document.
func TestAnOrganizationWithoutALicenseServesNoKey(t *testing.T) {
	assert.Empty(t, servedConfig(storedConfig(), nil).License)
	assert.Empty(t, servedConfig(storedConfig(), json.RawMessage(``)).License)
}

// The stored row is the caller's, loaded by the auth middleware. Writing the
// license into it would put the organization's license one Put away from
// being persisted on a sidecar.
func TestServingALicenseDoesNotTouchTheStoredConfiguration(t *testing.T) {
	stored := storedConfig()

	_ = servedConfig(stored, json.RawMessage(licenseDoc))

	assert.Empty(t, stored.License, "the stored configuration grew a license")
}

// The admin response is a different document from the one a sidecar reads.
// The UI renders it, and a license rendered there is a license an editor
// would eventually send back.
func TestTheAdminResponseCarriesNoLicense(t *testing.T) {
	resp := toResponse(models.Sidecar{ID: "sc-1", OrgID: "org-1", Name: "sc-a",
		Configuration: storedConfig()})

	assert.Empty(t, resp.Configuration.License)
}

// Refused rather than ignored. An admin who pastes a license into a sidecar
// configuration has to find out that it does nothing, or they will believe
// that sidecar is licensed.
func TestCreatingASidecarRefusesALicenseKey(t *testing.T) {
	w, c := newAdminRequest(t, http.MethodPost, map[string]any{
		"name": "sc-a",
		"configuration": map[string]any{
			"license":   licenseDoc,
			"listeners": []map[string]string{{"name": "appdb", "protocol": "postgres", "listen": ":1", "upstream": "h:5432"}},
		},
	})

	Post(c)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "does not belong in a sidecar configuration")
}

func TestUpdatingASidecarRefusesALicenseKey(t *testing.T) {
	w, c := newAdminRequest(t, http.MethodPut, map[string]any{
		"configuration": map[string]any{
			"license":   licenseDoc,
			"listeners": []map[string]string{{"name": "appdb", "protocol": "postgres", "listen": ":1", "upstream": "h:5432"}},
		},
	})
	c.Params = gin.Params{{Key: "nameOrID", Value: "sc-a"}}

	Put(c)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "does not belong in a sidecar configuration")
}

// newAdminRequest builds a request that already passed the auth middleware,
// so a handler runs against an org-scoped context. It stops short of the
// database: every case here is refused before the handler reaches one.
func newAdminRequest(t *testing.T, method string, body any) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/sidecars", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", "org-1").
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	return w, c
}
