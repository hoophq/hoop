package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mirrorStatus is the status GET /connections/{name} answers: the role page
// reads a mirror by name.
func mirrorStatus(t *testing.T, name string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/connections/"+name, nil)
	c.Params = gin.Params{{Key: "nameOrID", Value: name}}
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	apiconnections.Get(c)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var row struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &row))
	return row.Status
}

// A sidecar's handshake sets its mirrors online, and GET /connections/{name}
// reads them online.
func TestAHandshakeSetsTheMirrorsOnline(t *testing.T) {
	startSwitchDB(t)
	w, created := postSidecar(t, "pay", `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	assert.Equal(t, models.ConnectionStatusOffline, mirrorStatus(t, "pay-appdb"))

	sc, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, created.ID)
	require.NoError(t, err)
	w = handshake(t, sc, "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, models.ConnectionStatusOnline, mirrorStatus(t, "pay-appdb"))
}
