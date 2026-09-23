package apiroutes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
)

// A valid Enterprise document cannot be produced here: the signing key is not
// in the repository. These are the states that MUST be refused; the type
// alone, unsigned, is one of them, so a regression to a type-only check fails.
func TestEnterpriseLicenseOnlyRefuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		msg  string
		data string
	}{
		{"no license", ""},
		{"oss license", `{"payload":{"type":"oss","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`},
		{"enterprise type without a valid signature", `{"payload":{"type":"enterprise","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`},
		{"malformed document", `{"payload":`},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars", nil)
			ctx := storagev2.NewContext("user-1", "org-1")
			if tt.data != "" {
				ctx.WithOrgLicenseData(json.RawMessage(tt.data))
			}
			c.Set(storagev2.ContextKey, ctx)

			EnterpriseLicenseOnly(c)

			assert.True(t, c.IsAborted(), "handler reached")
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "enterprise license")
		})
	}
}
