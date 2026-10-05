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

const (
	identityIssuer = "https://issuer.example.com"
	firstSubject   = "system:serviceaccount:ws-1:hoop-sidecar"
	nextSubject    = "system:serviceaccount:ws-1-new:hoop-sidecar"
)

func callClearIdentity(t *testing.T, nameOrID string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/sidecars/"+nameOrID+"/identity", nil)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	ClearIdentity(c)
	// gin writes a status set with no body when the chain ends; a handler
	// called directly has no chain.
	c.Writer.WriteHeaderNow()
	return w.Code
}

func reach(name, subject string, adopt bool) (*models.Sidecar, error) {
	return models.GetOrCreateSidecarForIdentity(models.DB, switchOrgID, name, identityIssuer, subject,
		"service-account:"+subject, adopt)
}

// Clearing the binding lets the next identity that reaches the sidecar bind
// it. A sidecar made with a token keeps its token, and is bound again only
// through a mapping that adopts it.
func TestClearIdentityLetsTheNextIdentityBind(t *testing.T) {
	startSwitchDB(t)

	sc, err := reach("gke-eu-ws-1", firstSubject, false)
	require.NoError(t, err)
	_, err = reach("gke-eu-ws-1", nextSubject, false)
	require.ErrorIs(t, err, models.ErrSidecarBoundToAnotherIdentity)

	require.Equal(t, http.StatusNoContent, callClearIdentity(t, "gke-eu-ws-1"))
	cleared, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, cleared.IdentityIssuer)
	assert.Nil(t, cleared.IdentitySubject)

	rebound, err := reach("gke-eu-ws-1", nextSubject, false)
	require.NoError(t, err)
	assert.Equal(t, sc.ID, rebound.ID, "the binding moves, the sidecar stays")
	_, err = reach("gke-eu-ws-1", firstSubject, false)
	assert.ErrorIs(t, err, models.ErrSidecarBoundToAnotherIdentity, "the first identity no longer reaches it")

	const token = "hsc_adopted"
	made := &models.Sidecar{OrgID: switchOrgID, Name: "adopted", KeyHash: models.HashAPIKey(token), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, made))
	_, err = reach("adopted", firstSubject, true)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, callClearIdentity(t, made.ID))
	_, err = reach("adopted", nextSubject, false)
	assert.ErrorIs(t, err, models.ErrSidecarHasToken, "a cleared token sidecar needs adoption again")
	byToken, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(token))
	require.NoError(t, err)
	assert.Equal(t, made.ID, byToken.ID, "the token keeps working")

	assert.Equal(t, http.StatusNotFound, callClearIdentity(t, "no-such-sidecar"))
}

// A sidecar an identity created and an admin then cleared holds neither a
// token nor an identity. Its delete still records the name, or the next
// identity would create it again.
func TestDeleteRecordsTheNameOfAClearedIdentitySidecar(t *testing.T) {
	startSwitchDB(t)
	_, err := reach("gke-eu-ws-2", firstSubject, false)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, callClearIdentity(t, "gke-eu-ws-2"))

	require.Equal(t, http.StatusNoContent, callDelete(t, "gke-eu-ws-2"))
	names, err := models.ListSidecarDeletedNames(models.DB, switchOrgID)
	require.NoError(t, err)
	require.Len(t, names, 1)
	assert.Equal(t, "gke-eu-ws-2", names[0].Name)
	_, err = reach("gke-eu-ws-2", nextSubject, false)
	assert.ErrorIs(t, err, models.ErrSidecarNameDeleted)
}
