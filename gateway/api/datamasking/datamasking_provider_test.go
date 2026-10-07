package apigdatamasking

// appconfig.Load latches the environment on first call for the whole test
// binary, so every test in this package runs against ONE config state: no
// DLP provider configured.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
)

// loadNoProviderConfig loads the appconfig singleton with no DLP provider.
func loadNoProviderConfig(t *testing.T) {
	t.Helper()
	t.Setenv("POSTGRES_DB_URI", "postgres://hoop:secret@localhost:5432/hoop?sslmode=disable")
	t.Setenv("DLP_PROVIDER", "")
	t.Setenv("MSPRESIDIO_ANALYZER_URL", "")
	t.Setenv("MSPRESIDIO_ANONYMIZER_URL", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS_JSON", "")
	if err := appconfig.Load(); err != nil {
		t.Fatalf("appconfig.Load: %v", err)
	}
	if appconfig.Get().HasRedactCredentials() {
		t.Fatal("test invariant: this test binary must run without a DLP provider")
	}
}

func testContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/datamasking-rules", nil)
	return c, rec
}

// A rule the gateway enforces (no sidecar spec, or connections/attributes
// bound) needs the server's DLP provider; a rule only a sidecar runs needs
// none.
func TestRequireRedactProvider(t *testing.T) {
	loadNoProviderConfig(t)
	spec := json.RawMessage(`{"rules":[{"name":"email"}]}`)

	refused := []struct {
		name string
		req  *openapi.DataMaskingRuleRequest
		spec json.RawMessage
	}{
		{"no spec", &openapi.DataMaskingRuleRequest{}, nil},
		{"null spec", &openapi.DataMaskingRuleRequest{}, json.RawMessage("null")},
		{"spec and connections", &openapi.DataMaskingRuleRequest{ConnectionIDs: []string{"c1"}}, spec},
		{"spec and attributes", &openapi.DataMaskingRuleRequest{Attributes: []string{"pii"}}, spec},
	}
	for _, tt := range refused {
		c, rec := testContext()
		if requireRedactProvider(c, tt.req, tt.spec) {
			t.Fatalf("%s: expected a refusal without a DLP provider", tt.name)
		}
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: expected 422, got %d (body: %s)", tt.name, rec.Code, rec.Body.String())
		}
	}

	c, rec := testContext()
	if !requireRedactProvider(c, &openapi.DataMaskingRuleRequest{}, spec) {
		t.Fatal("sidecar-only rule: expected to pass without a DLP provider")
	}
	if c.Writer.Written() || rec.Body.Len() > 0 {
		t.Errorf("expected no response, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
