package apisidecar

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callDelete(t *testing.T, nameOrID string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/sidecars/"+nameOrID, nil)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	Delete(c)
	// gin writes a status set with no body when the chain ends; a handler
	// called directly has no chain.
	c.Writer.WriteHeaderNow()
	return w.Code
}

// Deleting a sidecar an identity reached records its name in the same
// transaction, so the identity's next heartbeat does not create it again. A
// token-made sidecar has no identity to stop and records nothing.
func TestDeleteRecordsTheNameOfAnIdentitySidecar(t *testing.T) {
	startSwitchDB(t)
	const sub = "system:serviceaccount:ws-1:hoop-sidecar"
	identity, err := models.GetOrCreateSidecarForIdentity(models.DB, switchOrgID, "gke-eu-ws-1",
		"https://issuer.example.com", sub, "service-account:"+sub)
	require.NoError(t, err)
	token := &models.Sidecar{OrgID: switchOrgID, Name: "token-made", KeyHash: models.HashAPIKey("hsc_token_made"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, token))

	require.Equal(t, http.StatusNoContent, callDelete(t, identity.ID))
	require.Equal(t, http.StatusNoContent, callDelete(t, token.Name))

	names, err := models.ListSidecarDeletedNames(models.DB, switchOrgID)
	require.NoError(t, err)
	require.Len(t, names, 1)
	assert.Equal(t, "gke-eu-ws-1", names[0].Name)
	assert.Equal(t, "admin@hoop.dev", names[0].DeletedBy)

	_, err = models.GetOrCreateSidecarForIdentity(models.DB, switchOrgID, "gke-eu-ws-1",
		"https://issuer.example.com", sub, "service-account:"+sub)
	assert.ErrorIs(t, err, models.ErrSidecarNameDeleted)

	assert.Equal(t, http.StatusNotFound, callDelete(t, token.Name))
}
