package apisidecar

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What the listener form saved in the field: the sidecar refuses every value
// below, so the save must be refused too.
const invalidSSHListener = `{"listeners": [{"name": "newdb", "protocol": "ssh", "listen": "0.0.0.0:1234",
	"ssh": {"host_key": "/etc/test", "trusted_ca": "/etc/trusted_ca",
		"destinations_allowed": ["10.30.10:1234"],
		"identity": {"subject": "key.id", "email": "email", "groups": "admion"}}}]}`

func TestAWriteTheSidecarWouldRefuseIsRefused(t *testing.T) {
	startSwitchDB(t)
	sc := &models.Sidecar{OrgID: switchOrgID, Name: "validated", KeyHash: models.HashAPIKey("hsc_validated"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, invalidSSHListener)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	var refused openapi.SidecarConfigError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refused))
	// One entry per problem, each naming its listener, so the form can list
	// them and tell a problem in another listener from one in its own.
	require.Len(t, refused.Problems, 4, "problems: %v", refused.Problems)
	for _, p := range refused.Problems {
		assert.True(t, strings.HasPrefix(p, "newdb: "), "problem without its listener: %q", p)
	}
	assert.Contains(t, refused.Problems[0], `"10.30.10:1234" is not a network`)

	// Files live on the sidecar host, so paths the gateway cannot open pass.
	valid := `{"listeners": [{"name": "jump", "protocol": "ssh", "listen": ":2222",
		"ssh": {"host_key": "/etc/test", "trusted_ca": "/etc/trusted_ca", "destinations_allowed": ["10.30.10.0/24:1234"],
			"identity": {"subject": "key_id", "groups": "principals"}}}]}`
	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, valid)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	// A patch is checked after the merge, and a refused one leaves the stored
	// document as it was.
	w, _ = callAdmin(t, Patch, http.MethodPatch, sc.ID, invalidSSHListener)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	require.Len(t, stored.Configuration.Listeners, 1)
	assert.Equal(t, "jump", stored.Configuration.Listeners[0].Name)
}
