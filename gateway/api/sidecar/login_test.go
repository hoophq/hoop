package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startLoginDB boots the embedded database with one organization, which
// holds no license: the free tier.
func startLoginDB(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	// Every test calls from the same address and user; a fresh budget each.
	loginStartLimiter = newKeyedLimiter(10, 5)
	loginCodeLimiter = newKeyedLimiter(20, 10)
	orgID := uuid.NewString()
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-login-test')`, orgID).Error)
	return orgID
}

func testSidecarToken(t *testing.T) string {
	t.Helper()
	return "hsc_" + strings.Repeat("a", 20) + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func callStartLogin(t *testing.T, token, name string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, _ := json.Marshal(map[string]string{"name": name})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/login", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	if token != "" {
		c.Request.Header.Set(apiroutes.SidecarTokenHeader, token)
	}
	StartLogin(c)
	return w
}

func callPollLogin(token string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/login/poll", nil)
	c.Request.Header.Set(apiroutes.SidecarTokenHeader, token)
	PollLogin(c)
	return w
}

// callDecide runs approve or deny as an admin of orgID.
func callDecide(orgID, code string, handler gin.HandlerFunc, body any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/login/"+code, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "code", Value: code}}
	handler(c)
	return w
}

func startedLogin(t *testing.T, token, name string) openapi.SidecarLoginResponse {
	t.Helper()
	w := callStartLogin(t, token, name)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	var resp openapi.SidecarLoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// The whole journey: start, wait, approve, and the token the sidecar made
// for itself is the one the plane now authenticates.
func TestSidecarLoginApprovedCreatesTheSidecarTheTokenReaches(t *testing.T) {
	orgID := startLoginDB(t)
	token := testSidecarToken(t)

	resp := startedLogin(t, token, "laptop")
	assert.Regexp(t, regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`), resp.UserCode)
	assert.Equal(t, "http://localhost:8009/hoop/sidecars/login?code="+resp.UserCode, resp.VerificationURL)
	assert.Equal(t, 600, resp.ExpiresIn)

	w := callPollLogin(token)
	assert.Equal(t, http.StatusPreconditionRequired, w.Code, "body: %s", w.Body)

	// Typed in lower case without the dash, the way a person copies it.
	typed := strings.ToLower(strings.ReplaceAll(resp.UserCode, "-", ""))
	w = callDecide(orgID, typed, ApproveLogin, map[string]string{"name": "edge-1"})
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)

	w = callPollLogin(token)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var status openapi.SidecarLoginStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.Equal(t, "approved", status.Status)
	assert.Equal(t, "edge-1", status.Name)

	sc, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(token))
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8009/hoop/sidecars/"+sc.ID, status.URL,
		"the sidecar names its own page, where its listeners are added")
	assert.Equal(t, orgID, sc.OrgID)
	assert.Equal(t, "edge-1", sc.Name)
	assert.Equal(t, "admin@hoop.dev", sc.CreatedBy)
}

// The plane keeps the hash and nothing it could hand back.
func TestSidecarLoginStoresOnlyTheTokenHash(t *testing.T) {
	startLoginDB(t)
	token := testSidecarToken(t)
	startedLogin(t, token, "laptop")

	var stored []string
	require.NoError(t, models.DB.Raw(`SELECT row_to_json(l)::text FROM private.sidecar_logins l`).Scan(&stored).Error)
	require.Len(t, stored, 1)
	assert.NotContains(t, stored[0], token)
	assert.Contains(t, stored[0], models.HashAPIKey(token))
}

func TestSidecarLoginRefusesAWeakOrMissingToken(t *testing.T) {
	startLoginDB(t)
	for _, token := range []string{"", "hsc_short", "hpk_" + strings.Repeat("a", 60)} {
		w := callStartLogin(t, token, "laptop")
		assert.Equal(t, http.StatusBadRequest, w.Code, "token %q, body: %s", token, w.Body)
	}
}

func TestSidecarLoginRefusesABadName(t *testing.T) {
	startLoginDB(t)
	w := callStartLogin(t, testSidecarToken(t), "x")
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
}

// One token, one login: a second start with it would make a second code that
// approves into the same key hash.
func TestSidecarLoginRefusesATokenThatAlreadyStarted(t *testing.T) {
	startLoginDB(t)
	token := testSidecarToken(t)
	startedLogin(t, token, "laptop")
	w := callStartLogin(t, token, "laptop")
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body)
}

func TestSidecarLoginDeniedStopsTheSidecar(t *testing.T) {
	orgID := startLoginDB(t)
	token := testSidecarToken(t)
	resp := startedLogin(t, token, "laptop")

	w := callDecide(orgID, resp.UserCode, DenyLogin, nil)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body)

	assert.Equal(t, http.StatusForbidden, callPollLogin(token).Code)
	// Decided once: an approval after the denial creates nothing.
	w = callDecide(orgID, resp.UserCode, ApproveLogin, nil)
	assert.Equal(t, http.StatusGone, w.Code, "body: %s", w.Body)
	_, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(token))
	assert.Error(t, err)
}

func TestSidecarLoginExpiredCannotBeApproved(t *testing.T) {
	orgID := startLoginDB(t)
	token := testSidecarToken(t)
	resp := startedLogin(t, token, "laptop")
	require.NoError(t, models.DB.Exec(`UPDATE private.sidecar_logins SET expires_at = ?`,
		time.Now().Add(-time.Minute)).Error)

	assert.Equal(t, http.StatusGone, callPollLogin(token).Code)
	w := callDecide(orgID, resp.UserCode, ApproveLogin, nil)
	assert.Equal(t, http.StatusGone, w.Code, "body: %s", w.Body)
}

func TestSidecarLoginUnknownTokenIsExpired(t *testing.T) {
	startLoginDB(t)
	assert.Equal(t, http.StatusGone, callPollLogin(testSidecarToken(t)).Code)
}

// The free tier holds one sidecar, whichever route creates the second.
func TestFreeOrganizationHoldsOneSidecar(t *testing.T) {
	orgID := startLoginDB(t)

	first := startedLogin(t, testSidecarToken(t), "first")
	require.Equal(t, http.StatusCreated, callDecide(orgID, first.UserCode, ApproveLogin, nil).Code)

	second := startedLogin(t, testSidecarToken(t), "second")
	w := callDecide(orgID, second.UserCode, ApproveLogin, nil)
	assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "free plan allows 1 sidecar")

	// The refused approval left the code pending, so it can be approved
	// once the seat is free.
	got, err := models.GetSidecarLoginByUserCode(models.DB, second.UserCode)
	require.NoError(t, err)
	assert.Equal(t, models.SidecarLoginPending, got.Status)

	// And the admin route that creates one with a token is capped too.
	gin.SetMode(gin.TestMode)
	raw, _ := json.Marshal(map[string]any{"name": "third"})
	pw := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(pw)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	Post(c)
	assert.Equal(t, http.StatusForbidden, pw.Code, "body: %s", pw.Body)
}

// An organization that lost a license with several sidecars keeps serving
// the oldest one; the rest stop at their next handshake.
func TestFreeOrganizationServesOnlyItsOldestSidecar(t *testing.T) {
	orgID := startLoginDB(t)
	older := &models.Sidecar{OrgID: orgID, Name: "older", KeyHash: models.HashAPIKey(testSidecarToken(t)), CreatedBy: "t"}
	require.NoError(t, models.CreateSidecar(models.DB, older))
	newer := &models.Sidecar{OrgID: orgID, Name: "newer", KeyHash: models.HashAPIKey(testSidecarToken(t)), CreatedBy: "t"}
	require.NoError(t, models.CreateSidecar(models.DB, newer))
	require.NoError(t, models.DB.Exec(`UPDATE private.sidecars SET created_at = created_at - interval '1 day' WHERE id = ?`, older.ID).Error)

	// The predicate apiroutes.SidecarSeatOnly answers 403 on.
	assert.NoError(t, services.CheckSidecarSeat(models.DB, orgID, older.ID, nil))
	assert.ErrorIs(t, services.CheckSidecarSeat(models.DB, orgID, newer.ID, nil), services.ErrSidecarSeatLimit)
}

// A decided code reads like an expired one: whoever guessed it learns
// neither the name nor the outcome.
func TestADecidedSidecarLoginCodeTellsNothing(t *testing.T) {
	orgID := startLoginDB(t)
	resp := startedLogin(t, testSidecarToken(t), "secret-host")
	require.Equal(t, http.StatusNoContent, callDecide(orgID, resp.UserCode, DenyLogin, nil).Code)

	w := callDecide(orgID, resp.UserCode, GetLogin, nil)
	assert.Equal(t, http.StatusGone, w.Code, "body: %s", w.Body)
	assert.NotContains(t, w.Body.String(), "secret-host")
	assert.NotContains(t, w.Body.String(), "denied")
}

// A token a sidecar already holds would fail at approve, after the person
// started; it is refused at the start instead.
func TestSidecarLoginRefusesATokenASidecarHolds(t *testing.T) {
	orgID := startLoginDB(t)
	token := testSidecarToken(t)
	require.NoError(t, models.CreateSidecar(models.DB, &models.Sidecar{
		OrgID: orgID, Name: "held", KeyHash: models.HashAPIKey(token), CreatedBy: "t"}))
	w := callStartLogin(t, token, "laptop")
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body)
}

func TestSidecarLoginIsRateLimited(t *testing.T) {
	orgID := startLoginDB(t)
	var last int
	for i := 0; i < 6; i++ {
		last = callStartLogin(t, testSidecarToken(t), "laptop").Code
	}
	assert.Equal(t, http.StatusTooManyRequests, last, "the sixth start from one address")

	for i := 0; i < 11; i++ {
		last = callDecide(orgID, "BCDF-GHJK", GetLogin, nil).Code
	}
	assert.Equal(t, http.StatusTooManyRequests, last, "the eleventh code read from one user")
}
