package apisidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func settleReview(sc *models.Sidecar, reviewID, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	c.Params = gin.Params{{Key: "id", Value: reviewID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews/"+reviewID+"/settle", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SettleReview(c)
	return rec
}

// reservedReview files raw, approves it and reserves the approval with a
// settling resend.
func reservedReview(t *testing.T, sc *models.Sidecar, raw []byte) string {
	t.Helper()
	filed := fileReview(t, sc, raw)
	approveAsDBA(t, filed.Review.ID)
	rec := postReviewReserving(sc, raw, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	require.True(t, resp.Forward, "the reservation releases the statement")
	require.Equal(t, openapi.ReviewStatusType(models.ReviewStatusProcessing), resp.Review.Status)
	require.Equal(t, filed.Review.ID, resp.Review.ID)
	return filed.Review.ID
}

// pastLease moves a reservation's start past the lease.
func pastLease(t *testing.T, reviewID string) {
	t.Helper()
	at := time.Now().UTC().Add(-models.SidecarReservationLease - time.Minute)
	require.NoError(t, models.DB.Exec(`UPDATE private.reviews SET reserved_at = ? WHERE id = ?`,
		at, reviewID).Error)
}

func TestSidecarReviewSettleRoutes(t *testing.T) {
	startStatusTestDB(t)
	t.Run("AcceptedSettleSpendsTheApproval", testAcceptedSettleSpendsTheApproval)
	t.Run("RefusedSettleGivesTheApprovalBack", testRefusedSettleGivesTheApprovalBack)
	t.Run("ClaimBodyReservesAndEmptyBodySpends", testClaimBodyReservesAndEmptyBodySpends)
	t.Run("LapsedReservationIsSpent", testLapsedReservationIsSpent)
	t.Run("ResendAfterALapsedReservationFilesANewReview", testResendAfterALapsedReservationFilesANewReview)
	t.Run("SettleIsScopedToTheSidecarAndItsReservation", testSettleIsScopedToTheSidecarAndItsReservation)
	t.Run("ListFiltersReadTheLease", testListFiltersReadTheLease)
	t.Run("ApproverCannotRevokeAReservation", testApproverCannotRevokeAReservation)
}

func testAcceptedSettleSpendsTheApproval(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-accept")
	raw := uniqueStatement()
	id := reservedReview(t, sc, raw)

	stored := readStoredReview(t, id)
	assert.Equal(t, string(models.ReviewStatusProcessing), stored.Status)
	assert.NotEqual(t, "done", stored.SessionStatus, "the session stays open until the settle")

	// A resend while the upstream answers matches the reservation and is
	// not released a second time.
	rec := postReviewReserving(sc, raw, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resend := decodeReviewResponse(t, rec)
	assert.False(t, resend.Forward)
	assert.Equal(t, id, resend.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusProcessing), resend.Review.Status)

	rec = settleReview(sc, id, `{"accepted":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	settled := decodeReviewResponse(t, rec)
	assert.False(t, settled.Forward)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), settled.Review.Status)
	assert.Equal(t, "done", readStoredReview(t, id).SessionStatus)

	// Spent: the same bytes need a new review.
	rec = postReviewReserving(sc, raw, true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.NotEqual(t, id, decodeReviewResponse(t, rec).Review.ID)
}

func testRefusedSettleGivesTheApprovalBack(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-refuse")
	raw := uniqueStatement()
	id := reservedReview(t, sc, raw)

	rec := settleReview(sc, id, `{"accepted":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), decodeReviewResponse(t, rec).Review.Status)
	stored := readStoredReview(t, id)
	assert.Equal(t, string(models.ReviewStatusApproved), stored.Status)
	assert.NotEqual(t, "done", stored.SessionStatus)

	// The resend, with a fresh credential, takes the same approval again.
	rec = postReviewReserving(sc, raw, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	again := decodeReviewResponse(t, rec)
	assert.True(t, again.Forward)
	assert.Equal(t, id, again.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusProcessing), again.Review.Status)

	// A second settle of a settled reservation moves nothing.
	rec = settleReview(sc, id, `{"accepted":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = settleReview(sc, id, `{"accepted":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), decodeReviewResponse(t, rec).Review.Status,
		"a spent approval is final")
}

func testClaimBodyReservesAndEmptyBodySpends(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-claim")

	reserved := fileReview(t, sc, uniqueStatement())
	approveAsDBA(t, reserved.Review.ID)
	rec := claimWithBody(sc, reserved.Review.ID, `{"reserve":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	assert.True(t, resp.Forward)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusProcessing), resp.Review.Status)

	// What a sidecar that predates settling sends: no body.
	spent := fileReview(t, sc, uniqueStatement())
	approveAsDBA(t, spent.Review.ID)
	rec = claimSidecarReview(sc, spent.Review.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp = decodeReviewResponse(t, rec)
	assert.True(t, resp.Forward)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), resp.Review.Status)
	assert.Equal(t, "done", readStoredReview(t, spent.Review.ID).SessionStatus)

	rec = claimWithBody(sc, spent.Review.ID, `{"reserve":`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func testLapsedReservationIsSpent(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-lapsed")
	id := reservedReview(t, sc, uniqueStatement())
	pastLease(t, id)

	rec := getReview(sc, id)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status openapi.SidecarReviewStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	assert.Equal(t, string(models.ReviewStatusExecuted), string(status.Status), "a lapsed lease reads as spent")
	assert.Equal(t, string(models.ReviewStatusProcessing), readStoredReview(t, id).Status, "a read writes nothing")

	// A late refusal cannot give the approval back: the upstream may have run it.
	rec = settleReview(sc, id, `{"accepted":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), decodeReviewResponse(t, rec).Review.Status)
	stored := readStoredReview(t, id)
	assert.Equal(t, string(models.ReviewStatusExecuted), stored.Status)
	assert.Equal(t, "done", stored.SessionStatus)
}

func testResendAfterALapsedReservationFilesANewReview(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-lapsed-resend")
	raw := uniqueStatement()
	id := reservedReview(t, sc, raw)
	pastLease(t, id)

	rec := postReviewReserving(sc, raw, true)
	require.Equal(t, http.StatusCreated, rec.Code, "the lapsed reservation leaves the index: %s", rec.Body.String())
	assert.NotEqual(t, id, decodeReviewResponse(t, rec).Review.ID)
	stored := readStoredReview(t, id)
	assert.Equal(t, string(models.ReviewStatusExecuted), stored.Status)
	assert.Equal(t, "done", stored.SessionStatus)
}

func testSettleIsScopedToTheSidecarAndItsReservation(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-owner")
	other := seedReviewingSidecar(t, "settle-stranger")
	id := reservedReview(t, sc, uniqueStatement())

	rec := settleReview(other, id, `{"accepted":false}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, string(models.ReviewStatusProcessing), readStoredReview(t, id).Status)

	rec = settleReview(sc, uuid.NewString(), `{"accepted":true}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = settleReview(sc, "not-a-uuid", `{"accepted":true}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = settleReview(sc, id, `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "accepted is required")

	// Only a reservation moves: a pending review answers as it stands.
	pending := fileReview(t, sc, uniqueStatement())
	rec = settleReview(sc, pending.Review.ID, `{"accepted":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), decodeReviewResponse(t, rec).Review.Status)
}

func testListFiltersReadTheLease(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-list")
	live := reservedReview(t, sc, uniqueStatement())
	lapsed := reservedReview(t, sc, uniqueStatement())
	pastLease(t, lapsed)

	ids := func(query string) []string {
		rec := listReviews(sc, query)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out []openapi.SidecarReviewStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		var got []string
		for _, r := range out {
			got = append(got, r.ID)
		}
		return got
	}
	assert.Equal(t, []string{live}, ids("?status=PROCESSING"))
	assert.Equal(t, []string{lapsed}, ids("?status=EXECUTED"))
}

func testApproverCannotRevokeAReservation(t *testing.T) {
	sc := seedReviewingSidecar(t, "settle-revoke")
	id := reservedReview(t, sc, uniqueStatement())

	rec := putReview(t, id, `{"status":"revoked"}`, []string{"dba"})
	assert.NotEqual(t, http.StatusOK, rec.Code, "the statement is already with the upstream: %s", rec.Body.String())
	assert.Equal(t, string(models.ReviewStatusProcessing), readStoredReview(t, id).Status)

	// Given back, it is an approval again and can be revoked.
	require.Equal(t, http.StatusOK, settleReview(sc, id, `{"accepted":false}`).Code)
	rec = putReview(t, id, `{"status":"revoked"}`, []string{"dba"})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
