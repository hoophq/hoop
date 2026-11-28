package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callOwners(t *testing.T, handler gin.HandlerFunc, method, nameOrID, body string) (*httptest.ResponseRecorder, openapi.SidecarOwners) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/sidecars/"+nameOrID+"/owners", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	handler(c)
	var resp openapi.SidecarOwners
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

func seedOwnerUser(t *testing.T, email, status string) {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, ?, ?)`, id, switchOrgID, "sub-"+id, email, email, status).Error)
}

// An owner is an email. It matches when exactly one user who may approve has
// it, whatever the case, and the list is stored as given: order kept,
// lowercase, no repeats.
func TestSidecarOwners(t *testing.T) {
	startSwitchDB(t)
	_, created := postSidecar(t, "owned", `{"listeners": []}`)
	require.NotEmpty(t, created.ID)
	seedOwnerUser(t, "Ana@Example.com", "active")
	seedOwnerUser(t, "invited@example.com", "invited")
	seedOwnerUser(t, "off@example.com", "inactive")
	seedOwnerUser(t, "twice@example.com", "active")
	seedOwnerUser(t, "TWICE@example.com", "active")

	t.Run("a sidecar starts with no owners", func(t *testing.T) {
		w, got := callOwners(t, GetOwners, http.MethodGet, "owned", "")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.JSONEq(t, `{"owners": []}`, w.Body.String())
		assert.Empty(t, got.Owners)
	})

	t.Run("the list is normalized, ordered and matched", func(t *testing.T) {
		w, got := callOwners(t, PutOwners, http.MethodPut, "owned", `{"owners": [
			" ANA@example.com ", "nobody@example.com", "ana@example.com",
			"invited@example.com", "off@example.com", "twice@example.com"]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		want := []openapi.SidecarOwner{
			{Email: "ana@example.com", Matched: true},
			{Email: "nobody@example.com", Matched: false},
			{Email: "invited@example.com", Matched: true},
			// An inactive user cannot approve, and two users identify nobody.
			{Email: "off@example.com", Matched: false},
			{Email: "twice@example.com", Matched: false},
		}
		assert.Equal(t, want, got.Owners)

		_, read := callOwners(t, GetOwners, http.MethodGet, created.ID, "")
		assert.Equal(t, want, read.Owners, "by id, the stored list")
	})

	t.Run("an owner matches once the user exists", func(t *testing.T) {
		seedOwnerUser(t, "nobody@example.com", "active")
		_, got := callOwners(t, GetOwners, http.MethodGet, "owned", "")
		require.Len(t, got.Owners, 5)
		assert.Equal(t, openapi.SidecarOwner{Email: "nobody@example.com", Matched: true}, got.Owners[1])
	})

	t.Run("a put replaces the list and an empty one clears it", func(t *testing.T) {
		_, got := callOwners(t, PutOwners, http.MethodPut, "owned", `{"owners": ["bo@example.com"]}`)
		assert.Equal(t, []openapi.SidecarOwner{{Email: "bo@example.com"}}, got.Owners)
		w, got := callOwners(t, PutOwners, http.MethodPut, "owned", `{"owners": []}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Empty(t, got.Owners)
	})

	t.Run("what is not a list of emails is refused and nothing changes", func(t *testing.T) {
		_, _ = callOwners(t, PutOwners, http.MethodPut, "owned", `{"owners": ["keep@example.com"]}`)
		many := make([]string, 0, maxSidecarOwners+1)
		for i := 0; i <= maxSidecarOwners; i++ {
			many = append(many, `"o`+strings.Repeat("x", i)+`@example.com"`)
		}
		for name, tc := range map[string]struct {
			body string
			code int
			want string
		}{
			"no owners key":     {`{}`, http.StatusBadRequest, "Owners"},
			"a display name":    {`{"owners": ["Ana <ana@example.com>"]}`, http.StatusUnprocessableEntity, "is not an email"},
			"not an email":      {`{"owners": ["ana"]}`, http.StatusUnprocessableEntity, "is not an email"},
			"an empty entry":    {`{"owners": [""]}`, http.StatusUnprocessableEntity, "is not an email"},
			"too many":          {`{"owners": [` + strings.Join(many, ",") + `]}`, http.StatusUnprocessableEntity, "at most 20 owners"},
			"an email too long": {`{"owners": ["` + strings.Repeat("a", 250) + `@example.com"]}`, http.StatusUnprocessableEntity, "over 255"},
		} {
			t.Run(name, func(t *testing.T) {
				w, _ := callOwners(t, PutOwners, http.MethodPut, "owned", tc.body)
				assert.Equal(t, tc.code, w.Code, "body: %s", w.Body)
				assert.Contains(t, w.Body.String(), tc.want)
			})
		}
		_, got := callOwners(t, GetOwners, http.MethodGet, "owned", "")
		assert.Equal(t, []openapi.SidecarOwner{{Email: "keep@example.com"}}, got.Owners)
	})

	t.Run("an unknown sidecar is not found", func(t *testing.T) {
		w, _ := callOwners(t, GetOwners, http.MethodGet, "missing", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		w, _ = callOwners(t, PutOwners, http.MethodPut, "missing", `{"owners": []}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("deleting the sidecar removes its owners", func(t *testing.T) {
		require.Equal(t, http.StatusNoContent, deleteSidecar(t, "owned").Code)
		var left int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.sidecar_owners WHERE sidecar_id = ?`, created.ID).Scan(&left).Error)
		assert.Zero(t, left)
	})
}
