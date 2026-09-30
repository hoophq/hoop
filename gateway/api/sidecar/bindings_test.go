package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listSidecars(t *testing.T) (*httptest.ResponseRecorder, []openapi.SidecarResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars", nil)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	List(c)
	var resp []openapi.SidecarResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

// The rule lists filter on these bindings, so a read that failed must not look
// like one that found nothing. The fleet page itself still answers.
func TestTheFleetSaysWhenItsBindingsCouldNotBeRead(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "payments")

	w, fleet := listSidecars(t)
	require.Equal(t, http.StatusOK, w.Code, "list: %s", w.Body)
	require.Len(t, fleet, 1)
	assert.NotEmpty(t, fleet[0].BoundRules, "the imported rules are bound to appdb")
	assert.NotContains(t, w.Body.String(), "bound_rules_unavailable", "a read that worked says nothing")

	require.NoError(t, models.DB.Exec(`DROP TABLE private.guardrail_rules_listeners`).Error)

	w, fleet = listSidecars(t)
	require.Equal(t, http.StatusOK, w.Code, "the fleet page still loads: %s", w.Body)
	require.Len(t, fleet, 1)
	assert.Empty(t, fleet[0].BoundRules)
	assert.True(t, fleet[0].BoundRulesUnavailable, "list: %s", w.Body)

	w, one := callAdmin(t, Get, http.MethodGet, sc.ID, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "get: %s", w.Body)
	assert.Empty(t, one.BoundRules)
	assert.True(t, one.BoundRulesUnavailable, "get: %s", w.Body)
}
