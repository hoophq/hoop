package apisidecar

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sshListener = `{"listeners": [{"name": "jump", "protocol": "ssh", "listen": ":2222",
	"ssh": {"host_key": "/etc/k", "trusted_ca": "/etc/ca.pub"}}]}`

func freshSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{OrgID: switchOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

func TestAWriteIsRefusedWhatTheSidecarDidNotReport(t *testing.T) {
	startSwitchDB(t)
	sc := freshSidecar(t, "old-build")
	keys := slices.DeleteFunc(daemon.ConfigKeys(), func(k string) bool { return strings.HasPrefix(k, "listeners.ssh") })
	protocols := slices.DeleteFunc(daemon.Protocols(), func(p string) bool { return p == "clickhouse" })
	require.NoError(t, models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "", "", "", keys, protocols))

	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, sshListener)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "listeners.ssh.host_key")

	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, `{"listeners": [
		{"name": "ch", "protocol": "clickhouse", "listen": ":9000", "upstream": "ch:9000"}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "the protocols clickhouse")

	w, resp := callAdmin(t, Put, http.MethodPut, sc.ID, `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432", "max_conns": 5}]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, protocols, resp.SupportedProtocols)

	w, _ = callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
		 "ssh": {"host_key": "/etc/k"}}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "listeners.ssh")
}

func TestASidecarThatNeverReportedIsNotGated(t *testing.T) {
	startSwitchDB(t)
	sc := freshSidecar(t, "never-connected")

	w, resp := callAdmin(t, Put, http.MethodPut, sc.ID, sshListener)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, resp.SupportedConfigKeys)
}
