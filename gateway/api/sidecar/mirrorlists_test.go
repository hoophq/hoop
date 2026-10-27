package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apiresources "github.com/hoophq/hoop/gateway/api/resources"
	apisearch "github.com/hoophq/hoop/gateway/api/search"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callAsAdmin runs handler on a GET to target, as an org admin.
func callAsAdmin(t *testing.T, handler gin.HandlerFunc, target string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	handler(c)
	return w
}

func names(t *testing.T, raw []byte, path ...string) []string {
	t.Helper()
	var body any
	require.NoError(t, json.Unmarshal(raw, &body))
	for _, key := range path {
		body = body.(map[string]any)[key]
	}
	var out []string
	for _, item := range body.([]any) {
		out = append(out, item.(map[string]any)["name"].(string))
	}
	sort.Strings(out)
	return out
}

// A sidecar mirror is the sidecar's, managed on the Sidecars page. No list
// shows it, so no screen offers it as a connection. It still answers by name.
func TestNoListShowsASidecarMirror(t *testing.T) {
	startSwitchDB(t)
	seedConnection(t, "plain")
	w, _ := postSidecar(t, "pay", `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
		{"name": "api", "protocol": "http", "listen": ":8080", "upstream": "api:80"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	// An admin put a connection of their own on the mirror's resource: the
	// resource stays listed, with that connection as its only role.
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name)
		VALUES (?, 'pay-api-ro', 'custom', 'redis', 'pay-api')`, switchOrgID).Error)

	w = callAsAdmin(t, apiconnections.List, "/api/connections", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, []string{"pay-api-ro", "plain"}, names(t, w.Body.Bytes()), "GET /connections")

	w = callAsAdmin(t, apiconnections.List, "/api/connections?page=1&page_size=10", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, []string{"pay-api-ro", "plain"}, names(t, w.Body.Bytes(), "data"), "GET /connections, paginated")
	assert.Contains(t, w.Body.String(), `"total":2`)

	w = callAsAdmin(t, apiconnections.List, "/api/connections?managed_by=sidecar", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, names(t, w.Body.Bytes()), "GET /connections?managed_by=sidecar")

	w = callAsAdmin(t, apiresources.ListResources, "/api/resources", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, []string{"pay-api", "plain"}, names(t, w.Body.Bytes()), "GET /resources")
	var resources []struct {
		Name  string `json:"name"`
		Roles []struct {
			Name string `json:"name"`
		} `json:"roles"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resources))
	for _, r := range resources {
		if r.Name != "pay-api" {
			continue
		}
		require.Len(t, r.Roles, 1, "GET /resources roles of pay-api")
		assert.Equal(t, "pay-api-ro", r.Roles[0].Name, "GET /resources roles of pay-api")
	}

	w = callAsAdmin(t, apisearch.Get, "/api/search?term=pay", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var found struct {
		Connections []any `json:"connections"`
		Resources   []any `json:"resources"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &found))
	assert.Len(t, found.Connections, 1, "GET /search connections: pay-api-ro only")
	assert.Len(t, found.Resources, 1, "GET /search resources: pay-api only")

	w = callAsAdmin(t, apiconnections.Get, "/api/connections/pay-appdb", gin.Params{{Key: "nameOrID", Value: "pay-appdb"}})
	assert.Equal(t, http.StatusOK, w.Code, "GET /connections/{name} still answers: %s", w.Body)
}
