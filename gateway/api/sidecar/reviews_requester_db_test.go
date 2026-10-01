package apisidecar

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postReviewBody files raw with requesterJSON as the requester value. Empty sends none.
func postReviewBody(t *testing.T, sc *models.Sidecar, raw []byte, requesterJSON string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	body := `{"listener_name":"appdb","approval_rule":"payments-approvers","payload":"` +
		base64.StdEncoding.EncodeToString(raw) + `"`
	if requesterJSON != "" {
		body += `,"requester":` + requesterJSON
	}
	body += `}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	PostReview(c)
	return rec
}

// putReviewAs runs PUT /reviews/:id as the given user, through the handler the API mounts.
func putReviewAs(t *testing.T, reviewID, body, userID, email string, groups []string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := storagev2.NewContext(userID, statusTestOrgID).WithUserInfo("User", email, "active", "", groups)
	c.Set(storagev2.ContextKey, ctx)
	c.Params = gin.Params{{Key: "id", Value: reviewID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/reviews/"+reviewID, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	reviewapi.NewHandler(func(orgID, sid, owner, status, reason, rejectedBy string) {}).ReviewByIdOrSid(c)
	return rec
}

// getSessionAsAdmin reads GET /sessions/:id, the call the review modal makes.
func getSessionAsAdmin(t *testing.T, sessionID string) map[string]json.RawMessage {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := storagev2.NewContext("admin-user", statusTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin})
	c.Set(storagev2.ContextKey, ctx)
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	sessionapi.Get(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func storedLabels(t *testing.T, sessionID string) map[string]string {
	t.Helper()
	sess, err := models.GetSessionByID(statusTestOrgID, sessionID)
	require.NoError(t, err)
	return sess.Labels
}

const aliceRequester = `{"subject":"alice","email":"alice@example.com","peer_addr":"10.0.0.1:5432","method":"database_user"}`

var aliceLabels = map[string]string{
	"sidecar.requester.subject":   "alice",
	"sidecar.requester.email":     "alice@example.com",
	"sidecar.requester.peer_addr": "10.0.0.1:5432",
	"sidecar.requester.method":    "database_user",
}

// One database for these cases: a PGlite instance keeps its memory until the
// test binary exits. Each case seeds its own sidecar.
func TestSidecarReviewRequester(t *testing.T) {
	startStatusTestDB(t)
	t.Run("PostReviewRecordsTheFirstRequester", testPostReviewRecordsTheFirstRequester)
	t.Run("PostReviewFromAnOlderSidecarRecordsNoRequester", testPostReviewFromAnOlderSidecarRecordsNoRequester)
	t.Run("PostReviewCleansAHostileRequester", testPostReviewCleansAHostileRequester)
	t.Run("PostReviewIgnoresAMalformedRequester", testPostReviewIgnoresAMalformedRequester)
	t.Run("AnApproverNamedAsTheRequesterMayStillApprove", testAnApproverNamedAsTheRequesterMayStillApprove)
	t.Run("TheRequesterCannotRejectOrRevoke", testTheRequesterCannotRejectOrRevoke)
	t.Run("TheRequesterOutlivesTheDecisionAndTheClaim", testTheRequesterOutlivesTheDecisionAndTheClaim)
}

// The review names its first filer. A later caller of the same bytes shares it and is not recorded.
func testPostReviewRecordsTheFirstRequester(t *testing.T) {
	sc := seedReviewingSidecar(t, "first-filer")
	raw := []byte("DELETE FROM users WHERE id = 1;")

	rec := postReviewBody(t, sc, raw, aliceRequester)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	first := decodeReviewResponse(t, rec)
	assert.NotContains(t, rec.Body.String(), "requester", "the sidecar never gets the filer back")

	rec = postReviewBody(t, sc, raw, `{"subject":"bob","method":"database_user"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	again := decodeReviewResponse(t, rec)
	assert.Equal(t, first.Review.ID, again.Review.ID)

	assert.Equal(t, aliceLabels, storedLabels(t, first.Review.Session))

	var labels map[string]string
	require.NoError(t, json.Unmarshal(getSessionAsAdmin(t, first.Review.Session)["labels"], &labels))
	assert.Equal(t, aliceLabels, labels, "the review modal reads the filer from GET /sessions/:id")

	rev, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, first.Review.ID)
	require.NoError(t, err)
	assert.Equal(t, sc.ID, rev.OwnerID, "the filer never becomes the owner")
	assert.Equal(t, reviewOwnerEmail, rev.OwnerEmail)
	sess, err := models.GetSessionByID(statusTestOrgID, first.Review.Session)
	require.NoError(t, err)
	assert.Equal(t, sc.ID, sess.UserID, "the session user stays the sidecar")
	assert.Equal(t, reviewOwnerEmail, sess.UserEmail)

	// The approval is shared: bob's retry consumes it, and the review still names alice.
	rec = putReviewAs(t, first.Review.ID, `{"status":"approved"}`, "dba-user", "dba@hoop.dev", []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = postReviewBody(t, sc, raw, `{"subject":"bob","method":"database_user"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	consumed := decodeReviewResponse(t, rec)
	assert.True(t, consumed.Forward)
	assert.Equal(t, first.Review.ID, consumed.Review.ID)
	assert.Equal(t, aliceLabels, storedLabels(t, first.Review.Session))
}

func testPostReviewFromAnOlderSidecarRecordsNoRequester(t *testing.T) {
	sc := seedReviewingSidecar(t, "older")

	rec := postReview(sc, []byte("DELETE FROM users WHERE id = 2;"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)

	assert.Nil(t, storedLabels(t, resp.Review.Session))
	assert.Equal(t, "null", string(getSessionAsAdmin(t, resp.Review.Session)["labels"]))
}

// Hostile values are cleaned and clipped, never refused.
func testPostReviewCleansAHostileRequester(t *testing.T) {
	sc := seedReviewingSidecar(t, "hostile")

	hostile, err := json.Marshal(map[string]string{
		"subject":   "a\x00b‮c​d" + strings.Repeat("é", 512*1024),
		"email":     "\x1b[31m" + strings.Repeat("x", 300),
		"peer_addr": " 10.0.0.1:5432\n",
		"method":    "GOOGLE_IDENTITY",
	})
	require.NoError(t, err)
	onlyPeer := `{"peer_addr":"10.0.0.9:1234","method":"GOOGLE_IDENTITY"}`

	for name, tc := range map[string]struct {
		requester  string
		wantMethod string
	}{
		"control runes and a 1 MiB subject": {string(hostile), "google_identity"},
		"a method with no name":             {onlyPeer, "peer_address"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := postReviewBody(t, sc, []byte("DELETE FROM t WHERE k = '"+name+"';"), tc.requester)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			resp := decodeReviewResponse(t, rec)

			labels := storedLabels(t, resp.Review.Session)
			require.NotEmpty(t, labels)
			for _, v := range labels {
				assertCleanValue(t, v)
			}
			assert.Equal(t, tc.wantMethod, labels["sidecar.requester.method"])
		})
	}
}

// A requester of the wrong shape is ignored, and the review is still filed.
func testPostReviewIgnoresAMalformedRequester(t *testing.T) {
	sc := seedReviewingSidecar(t, "malformed")

	for i, requester := range []string{`"alice"`, `{"subject":1}`, `[1,2]`} {
		rec := postReviewBody(t, sc, []byte("DELETE FROM t WHERE k = "+string(rune('a'+i))+";"), requester)
		require.Equal(t, http.StatusCreated, rec.Code, "%s: %s", requester, rec.Body.String())
		resp := decodeReviewResponse(t, rec)
		assert.Nil(t, storedLabels(t, resp.Review.Session), requester)
	}
}

// The filer is display data: a reviewer it names is not the owner, so it may approve.
func testAnApproverNamedAsTheRequesterMayStillApprove(t *testing.T) {
	sc := seedReviewingSidecar(t, "approver")
	const approverID = "7a1e0000-0000-0000-0000-00000000a11c"

	rec := postReviewBody(t, sc, []byte("DELETE FROM users WHERE id = 3;"),
		`{"subject":"`+approverID+`","email":"reviewer@hoop.dev","method":"identity_header"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)

	rec = putReviewAs(t, resp.Review.ID, `{"status":"approved"}`, approverID, "reviewer@hoop.dev", []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body openapi.Review
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), body.Status)
}

// Owner-may-reject reads OwnerID, which stays the sidecar. The filer gains no right from it.
func testTheRequesterCannotRejectOrRevoke(t *testing.T) {
	sc := seedReviewingSidecar(t, "no-owner-rights")
	const filerID = "7a1e0000-0000-0000-0000-0000000f11e5"
	filer := `{"subject":"` + filerID + `","email":"filer@hoop.dev","method":"google_identity"}`

	t.Run("reject a pending review", func(t *testing.T) {
		rec := postReviewBody(t, sc, []byte("DELETE FROM users WHERE id = 4;"), filer)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		resp := decodeReviewResponse(t, rec)

		rec = putReviewAs(t, resp.Review.ID, `{"status":"rejected"}`, filerID, "filer@hoop.dev", []string{"developers"})
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"not eligible for review"}`, rec.Body.String())

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, resp.Review.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusPending, got.Status)
	})

	t.Run("revoke an approved review", func(t *testing.T) {
		rec := postReviewBody(t, sc, []byte("DELETE FROM users WHERE id = 5;"), filer)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		resp := decodeReviewResponse(t, rec)
		rec = putReviewAs(t, resp.Review.ID, `{"status":"approved"}`, "dba-user", "dba@hoop.dev", []string{"dba"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		rec = putReviewAs(t, resp.Review.ID, `{"status":"revoked"}`, filerID, "filer@hoop.dev", []string{"developers"})
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"not eligible for review"}`, rec.Body.String())

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, resp.Review.ID)
		require.NoError(t, err)
		assert.Equal(t, models.ReviewStatusApproved, got.Status)
	})
}

// Nothing writes the labels after the review is filed: not a decision, not a claim.
func testTheRequesterOutlivesTheDecisionAndTheClaim(t *testing.T) {
	sc := seedReviewingSidecar(t, "outlives")
	raw := []byte("DELETE FROM users WHERE id = 6;")

	rec := postReviewBody(t, sc, raw, aliceRequester)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)

	rec = putReviewAs(t, resp.Review.ID, `{"status":"approved"}`, "dba-user", "dba@hoop.dev", []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, aliceLabels, storedLabels(t, resp.Review.Session), "after the decision")

	rec = claimSidecarReview(sc, resp.Review.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	claim := decodeReviewResponse(t, rec)
	assert.True(t, claim.Forward)
	assert.Equal(t, aliceLabels, storedLabels(t, resp.Review.Session), "after the claim")

	// A resend by another caller files a new review under its own name.
	rec = postReviewBody(t, sc, raw, `{"subject":"bob","method":"database_user"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	next := decodeReviewResponse(t, rec)
	assert.NotEqual(t, resp.Review.ID, next.Review.ID)
	assert.Equal(t, "bob", storedLabels(t, next.Review.Session)["sidecar.requester.subject"])
	assert.Equal(t, aliceLabels, storedLabels(t, resp.Review.Session), "after the next filing")
}
