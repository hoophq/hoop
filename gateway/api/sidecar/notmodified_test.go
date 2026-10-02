package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handshakeRow runs the handshake the way SidecarAuthMiddleware hands it over:
// the row it loaded, with the org gen read beside it.
func handshakeRow(t *testing.T, name string, req openapi.SidecarHandshakeRequest) *httptest.ResponseRecorder {
	t.Helper()
	sc, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey("hsc_"+name))
	require.NoError(t, err)
	body, err := json.Marshal(req)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("sidecar-auth", sc)
	Handshake(c)
	return w
}

// The plane answers 304 only while every compose input is the one the named
// document was composed from. Each case changes one input after a full
// answer and expects the next handshake to compose again.
func TestTheHandshakeAnswersNotModifiedOnlyWhileNothingChanged(t *testing.T) {
	startSwitchDB(t)
	featureflag.Set(switchOrgID, notModifiedFlag, true)
	t.Cleanup(func() { featureflag.Set(switchOrgID, notModifiedFlag, false) })

	for _, tc := range []struct {
		name    string
		change  func(t *testing.T, sc *models.Sidecar)
		version string
		want    int
	}{
		{name: "nm-unchanged", want: http.StatusNotModified},
		{name: "nm-rule-edit", want: http.StatusOK, change: func(t *testing.T, _ *models.Sidecar) {
			require.NoError(t, models.DB.Exec(
				`UPDATE private.guardrail_rules SET sidecar_spec = sidecar_spec WHERE org_id = ?`, switchOrgID).Error)
		}},
		{name: "nm-new-version", version: "1.2.4", want: http.StatusOK},
		{name: "nm-stale-compose", want: http.StatusOK, change: func(t *testing.T, sc *models.Sidecar) {
			require.NoError(t, models.DB.Exec(
				`UPDATE private.sidecars SET composed_at = NOW() - INTERVAL '11 minutes' WHERE id = ?`, sc.ID).Error)
		}},
		{name: "nm-plane-refusal", want: http.StatusOK, change: func(t *testing.T, sc *models.Sidecar) {
			require.NoError(t, models.RecordSidecarServeRefusal(models.DB, sc.ID, "1.2.3", nil, "too old"))
		}},
		{name: "nm-flag-off", want: http.StatusOK, change: func(t *testing.T, _ *models.Sidecar) {
			featureflag.Set(switchOrgID, notModifiedFlag, false)
			t.Cleanup(func() { featureflag.Set(switchOrgID, notModifiedFlag, true) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := importedSidecar(t, tc.name)
			first := handshakeRow(t, tc.name, openapi.SidecarHandshakeRequest{Version: "1.2.3"})
			require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body)
			revision := first.Header().Get(daemon.ConfigRevisionHeader)
			require.NotEmpty(t, revision)

			if tc.change != nil {
				tc.change(t, sc)
			}
			version := "1.2.3"
			if tc.version != "" {
				version = tc.version
			}
			w := handshakeRow(t, tc.name, openapi.SidecarHandshakeRequest{
				Version: version, AppliedRevision: revision, LastOutcome: "applied", ServedRevision: revision})
			require.Equal(t, tc.want, w.Code, "body: %s", w.Body)
			assert.Equal(t, revision, w.Header().Get(daemon.ConfigRevisionHeader))
			assert.Equal(t, "true", w.Header().Get(licenseManagedHeader))
			if tc.want == http.StatusNotModified {
				assert.Empty(t, w.Body.String(), "a 304 carries no document")
			} else {
				assert.True(t, strings.Contains(w.Body.String(), `"listeners"`), "a full answer carries the document")
			}
		})
	}

	// A sidecar that names no served revision, as one older than the field
	// does, always receives the document. Same database: each boot costs tens
	// of seconds under make test-oss's 15 minute timeout.
	t.Run("nm-old-build", func(t *testing.T) {
		importedSidecar(t, "nm-old-build")
		for range 2 {
			w := handshakeRow(t, "nm-old-build", openapi.SidecarHandshakeRequest{Version: "1.2.3"})
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
			assert.Contains(t, w.Body.String(), `"listeners"`)
		}
	})
}
