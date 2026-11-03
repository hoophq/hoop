package apisidecar

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callAsSidecar runs handler behind the real SidecarAuthMiddleware, so the
// instance header travels the way it does from a sidecar. An empty instance
// is a build too old to send the header.
func callAsSidecar(handler gin.HandlerFunc, path, token, instance string, body []byte) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST(path, (&apiroutes.Router{}).SidecarAuthMiddleware, handler)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiroutes.SidecarTokenHeader, token)
	if instance != "" {
		req.Header.Set(daemon.SidecarInstanceHeader, instance)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func handshakeFrom(token, instance string) *httptest.ResponseRecorder {
	return callAsSidecar(Handshake, "/api/sidecars/handshake", token, instance,
		[]byte(`{"version": "1.191.0", "applied_revision": "rev-a", "last_outcome": "applied"}`))
}

// Pods sharing one token reach one sidecar row and are listed apart under
// instances; a build that sends no header is not listed, as before.
func TestAHandshakeRecordsItsInstance(t *testing.T) {
	startSwitchDB(t)
	w, created := postSidecar(t, "pods", `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)

	w = handshakeFrom(created.Token, "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	_, got := callAdmin(t, Get, http.MethodGet, created.ID, "{}")
	assert.Nil(t, got.Instances, "a build without the header lists no instance")

	w = handshakeFrom(created.Token, " pods-7d9f-b ")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	w = handshakeFrom(created.Token, "pods-7d9f-a")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	w = handshakeFrom(created.Token, "pods-7d9f-a")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	_, got = callAdmin(t, Get, http.MethodGet, created.ID, "{}")
	require.Len(t, got.Instances, 2, "one row per pod, by name")
	for i, name := range []string{"pods-7d9f-a", "pods-7d9f-b"} {
		inst := got.Instances[i]
		assert.Equal(t, name, inst.Name, "trimmed and sorted")
		assert.Equal(t, openapi.SidecarInstanceOnline, inst.Status)
		assert.Equal(t, "1.191.0", inst.Version)
		assert.Equal(t, "rev-a", inst.AppliedRevision)
		assert.Equal(t, "applied", inst.LastOutcome)
		assert.Empty(t, inst.LastError)
		assert.False(t, inst.LastSeenAt.IsZero())
	}

	require.NoError(t, models.DB.Exec(`UPDATE private.sidecar_instances SET last_seen_at = NOW() - interval '4 minutes' WHERE name = 'pods-7d9f-b'`).Error)
	_, got = callAdmin(t, Get, http.MethodGet, created.ID, "{}")
	require.Len(t, got.Instances, 2)
	assert.Equal(t, openapi.SidecarInstanceOnline, got.Instances[0].Status)
	assert.Equal(t, openapi.SidecarInstanceOffline, got.Instances[1].Status, "silent past SidecarMirrorOfflineAfter")

	w = handshakeFrom(created.Token, string(bytes.Repeat([]byte("h"), 254)))
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), daemon.SidecarInstanceHeader)
}

// A session a pod records names the pod, so an audit of one sidecar with many
// pods can tell which process ran the statement.
func TestPostEventsRecordsTheInstance(t *testing.T) {
	startEventsDB(t, eventsInstanceOrgID)
	enableSessionEvents(t, eventsInstanceOrgID)
	sc := seedEventsSidecar(t, eventsInstanceOrgID, "edge-inst")

	w := callAsSidecar(PostEvents, "/api/sidecars/events", "hsc_edge-inst", "edge-inst-7d9f-a",
		eventsBody(t, fullSession("s-inst")...))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	meta := sidecarMetadata(t, getSidecarSessionRow(t, eventsInstanceOrgID, services.SidecarSessionID(sc.ID, "s-inst")))
	assert.Equal(t, "edge-inst-7d9f-a", meta["instance"])

	w = callAsSidecar(PostEvents, "/api/sidecars/events", "hsc_edge-inst", "",
		eventsBody(t, fullSession("s-old")...))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	meta = sidecarMetadata(t, getSidecarSessionRow(t, eventsInstanceOrgID, services.SidecarSessionID(sc.ID, "s-old")))
	_, named := meta["instance"]
	assert.False(t, named, "a build without the header names no instance")
}
