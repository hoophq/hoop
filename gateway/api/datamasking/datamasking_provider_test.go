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
	if err := appconfig.Load(appconfig.AppModeGateway); err != nil {
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

func TestRequireRedactProviderRejectsWithoutProvider(t *testing.T) {
	loadNoProviderConfig(t)

	c, rec := testContext()
	if requireRedactProvider(c) {
		t.Fatal("expected requireRedactProvider to reject when no provider is configured")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// The guard must run before any payload parsing or database access, so the
// full handlers are safe to invoke with no body and no database in this
// state.
func TestHandlersRejectedBeforeTouchingDatabase(t *testing.T) {
	loadNoProviderConfig(t)

	c, rec := testContext()
	Post(c)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("Post without provider: expected 422, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	c, rec = testContext()
	Put(c)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("Put without provider: expected 422, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// On the control plane, a rule with a sidecar spec needs no DLP provider. A
// rule without one keeps the check.
func TestControlPlaneProviderError(t *testing.T) {
	loadNoProviderConfig(t)

	if err := controlPlaneProviderError(json.RawMessage(`{"rules":[{"name":"email"}]}`)); err != nil {
		t.Errorf("rule with a sidecar spec: expected no error, got %v", err)
	}
	for _, spec := range []json.RawMessage{nil, json.RawMessage("null")} {
		if err := controlPlaneProviderError(spec); err == nil {
			t.Errorf("sidecar spec %q: expected an error without a DLP provider", spec)
		}
	}
}

// The control plane check must not change the gateway: it writes nothing and
// lets the handler continue.
func TestControlPlaneProviderIsNoOpOnGateway(t *testing.T) {
	loadNoProviderConfig(t)

	c, rec := testContext()
	if !requireControlPlaneProvider(c, nil) {
		t.Fatal("expected requireControlPlaneProvider to pass on the gateway")
	}
	if c.Writer.Written() || rec.Body.Len() > 0 {
		t.Errorf("expected no response on the gateway, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
