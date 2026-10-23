package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminJSON sends body to handler as an admin of the switch org.
func adminJSON(t *testing.T, handler gin.HandlerFunc, method, nameOrID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/sidecars/"+nameOrID, bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	if nameOrID != "" {
		c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	}
	handler(c)
	return w
}

func sidecarBody(t *testing.T, w *httptest.ResponseRecorder) openapi.SidecarResponse {
	t.Helper()
	var resp openapi.SidecarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// The owner is a column beside the document: POST sets it, GET returns it,
// a PUT without it keeps it, an empty one clears it, and a value that is not
// an address is refused.
func TestSidecarOwnerEmail(t *testing.T) {
	startSwitchDB(t)

	w := adminJSON(t, Post, http.MethodPost, "", `{"name":"owned","owner_email":" dba@acme.com ","configuration":{}}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	assert.Equal(t, "dba@acme.com", sidecarBody(t, w).OwnerEmail, "trimmed")

	w = adminJSON(t, Get, http.MethodGet, "owned", "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, "dba@acme.com", sidecarBody(t, w).OwnerEmail)

	w = adminJSON(t, Put, http.MethodPut, "owned", `{"configuration":{}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, "dba@acme.com", sidecarBody(t, w).OwnerEmail, "omitted keeps the owner")

	w = adminJSON(t, Put, http.MethodPut, "owned", `{"configuration":{},"owner_email":"sre@acme.com"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, "sre@acme.com", sidecarBody(t, w).OwnerEmail)

	w = adminJSON(t, Put, http.MethodPut, "owned", `{"configuration":{},"owner_email":"Not An Email"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "owner_email")

	w = adminJSON(t, Put, http.MethodPut, "owned", `{"configuration":{},"owner_email":""}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, sidecarBody(t, w).OwnerEmail, "empty clears it")

	w = adminJSON(t, Post, http.MethodPost, "", `{"name":"bad-owner","owner_email":"Dba <dba@acme.com>","configuration":{}}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
}
