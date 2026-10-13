package apisidecar

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// decodeReviewResponse reads the sidecar review answer a handler wrote.
func decodeReviewResponse(t *testing.T, rec *httptest.ResponseRecorder) openapi.SidecarReviewResponse {
	t.Helper()
	var resp openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	require.NotNil(t, resp.Review)
	return resp
}

// setRuleTTLs sets the limits of payments-approvers, the rule the cases share,
// until the case ends. A review copies them when it is filed.
func setRuleTTLs(t *testing.T, pending, approval *int) {
	t.Helper()
	set := func(pending, approval *int) error {
		return models.DB.Exec(`
		UPDATE private.access_request_rules SET pending_ttl_sec = ?, approval_ttl_sec = ?
		WHERE org_id = ? AND name = 'payments-approvers'`, pending, approval, statusTestOrgID).Error
	}
	require.NoError(t, set(pending, approval))
	t.Cleanup(func() { require.NoError(t, set(nil, nil)) })
}

// pastDeadline moves a review's deadline a minute into the past and returns it.
// The column has no time zone, so the value is bound in UTC.
func pastDeadline(t *testing.T, reviewID string) time.Time {
	t.Helper()
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	require.NoError(t, models.DB.Exec(`UPDATE private.reviews SET expires_at = ? WHERE id = ?`,
		at, reviewID).Error)
	return at
}

type storedTTLReview struct {
	Status        string
	StatementHash sql.NullString
	ExpiresAt     *time.Time
	SessionStatus string
}

// readStoredReview reads the columns as stored, past the in-memory EXPIRED.
func readStoredReview(t *testing.T, reviewID string) storedTTLReview {
	t.Helper()
	var row storedTTLReview
	require.NoError(t, models.DB.Raw(`
	SELECT r.status, r.statement_hash, r.expires_at, s.status AS session_status
	FROM private.reviews r JOIN private.sessions s ON s.org_id = r.org_id AND s.id = r.session_id
	WHERE r.id = ?`, reviewID).Scan(&row).Error)
	return row
}

func fileReview(t *testing.T, sc *models.Sidecar, raw []byte) openapi.SidecarReviewResponse {
	t.Helper()
	rec := postReview(sc, raw)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeReviewResponse(t, rec)
}

func approveAsDBA(t *testing.T, reviewID string) openapi.Review {
	t.Helper()
	rec := putReview(t, reviewID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body openapi.Review
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, openapi.ReviewStatusType(models.ReviewStatusApproved), body.Status)
	return body
}

func uniqueStatement() []byte {
	return []byte("DELETE FROM users WHERE id = '" + uuid.NewString() + "';")
}

// One database for these cases: a PGlite instance keeps its memory until the
// test binary exits. Each case seeds its own sidecar.
func TestSidecarReviewTTLRoutes(t *testing.T) {
	startStatusTestDB(t)
	t.Run("PostReviewRefilesAnExpiredReview", testPostReviewRefilesAnExpiredReview)
	t.Run("PostReviewNeverReleasesAnExpiredApproval", testPostReviewNeverReleasesAnExpiredApproval)
	t.Run("ClaimReviewAnswersExpired", testClaimReviewAnswersExpired)
	t.Run("ClaimAfterALateApprovalReleases", testClaimAfterALateApprovalReleases)
	t.Run("AStaleClaimAnswersExpired", testAStaleClaimAnswersExpired)
	t.Run("GetReviewReportsExpiryWithoutWriting", testGetReviewReportsExpiryWithoutWriting)
	t.Run("PutReviewRefusesAnExpiredReview", testPutReviewRefusesAnExpiredReview)
	t.Run("PutReviewAfterTheRowExpired", testPutReviewAfterTheRowExpired)
	t.Run("PutReviewStartsTheApprovalClock", testPutReviewStartsTheApprovalClock)
	t.Run("ReviewsWithoutTTLKeepTodaysAnswers", testReviewsWithoutTTLKeepTodaysAnswers)
}

func testPostReviewRefilesAnExpiredReview(t *testing.T) {
	sc := seedReviewingSidecar(t, "refiler")
	setRuleTTLs(t, ptr.Int(900), nil)
	raw := uniqueStatement()

	first := fileReview(t, sc, raw)
	require.NotNil(t, first.Review.ExpiresAt, "the rule's pending limit sets a deadline")
	assert.WithinDuration(t, first.Review.CreatedAt.Add(900*time.Second), *first.Review.ExpiresAt, time.Second)

	rec := postReview(sc, raw)
	require.Equal(t, http.StatusOK, rec.Code, "inside the deadline a retry answers the same review: %s", rec.Body.String())
	assert.Equal(t, first.Review.ID, decodeReviewResponse(t, rec).Review.ID)

	pastDeadline(t, first.Review.ID)
	rec = postReview(sc, raw)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	second := decodeReviewResponse(t, rec)
	assert.NotEqual(t, first.Review.ID, second.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), second.Review.Status)
	assert.False(t, second.Forward)

	old := readStoredReview(t, first.Review.ID)
	assert.Equal(t, string(models.ReviewStatusExpired), old.Status)
	assert.False(t, old.StatementHash.Valid, "the hash is cleared, so the new review takes the index")
	assert.Equal(t, "done", old.SessionStatus)
}

func testPostReviewNeverReleasesAnExpiredApproval(t *testing.T) {
	sc := seedReviewingSidecar(t, "late-resend")
	setRuleTTLs(t, ptr.Int(900), ptr.Int(600))
	raw := uniqueStatement()

	first := fileReview(t, sc, raw)
	approveAsDBA(t, first.Review.ID)
	pastDeadline(t, first.Review.ID)

	rec := postReview(sc, raw)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	assert.False(t, resp.Forward, "a lapsed approval never releases the statement")
	assert.NotEqual(t, first.Review.ID, resp.Review.ID)
	assert.Equal(t, string(models.ReviewStatusExpired), readStoredReview(t, first.Review.ID).Status)
}

func testClaimReviewAnswersExpired(t *testing.T) {
	sc := seedReviewingSidecar(t, "claimer")
	setRuleTTLs(t, ptr.Int(900), ptr.Int(600))

	for name, approve := range map[string]bool{"a pending review": false, "an approved review": true} {
		t.Run(name, func(t *testing.T) {
			first := fileReview(t, sc, uniqueStatement())
			if approve {
				approveAsDBA(t, first.Review.ID)
			}
			deadline := pastDeadline(t, first.Review.ID)

			rec := claimSidecarReview(sc, first.Review.ID)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			resp := decodeReviewResponse(t, rec)
			assert.False(t, resp.Forward)
			assert.Equal(t, openapi.ReviewStatusExpired, resp.Review.Status)
			if assert.NotNil(t, resp.Review.ExpiresAt, "the deadline that passed") {
				assert.True(t, deadline.Equal(*resp.Review.ExpiresAt), "want %v, got %v", deadline, *resp.Review.ExpiresAt)
			}

			stored := readStoredReview(t, first.Review.ID)
			assert.Equal(t, string(models.ReviewStatusExpired), stored.Status, "the claim records the expiry")
			assert.False(t, stored.StatementHash.Valid)
			assert.Equal(t, "done", stored.SessionStatus)

			snap := &models.Review{ID: first.Review.ID, SessionID: first.Review.Session}
			before := reviewSnapshot(t, snap)
			rec = claimSidecarReview(sc, first.Review.ID)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			again := decodeReviewResponse(t, rec)
			assert.False(t, again.Forward)
			assert.Equal(t, openapi.ReviewStatusExpired, again.Review.Status)
			assert.JSONEq(t, before, reviewSnapshot(t, snap), "a second claim writes nothing")
		})
	}
}

// A claim reads the review and its time, then can wait on the row lock past the
// deadline. The handler gets that read: the database clock refuses the claim,
// and the answer is EXPIRED, never APPROVED with forward false.
func testAStaleClaimAnswersExpired(t *testing.T) {
	sc := seedReviewingSidecar(t, "stale-claimer")
	setRuleTTLs(t, ptr.Int(900), ptr.Int(600))
	first := fileReview(t, sc, uniqueStatement())
	approveAsDBA(t, first.Review.ID)

	rev, err := models.GetSidecarReview(models.DB, sc.OrgID, sc.ID, first.Review.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewStatusApproved, rev.Status, "read inside the deadline")
	// Bound before the deadline pastDeadline sets a minute ago.
	now := time.Now().UTC().Add(-2 * time.Minute)
	pastDeadline(t, first.Review.ID)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	answerExistingReview(c, sc, rev.ListenerName.String, rev, now)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	assert.False(t, resp.Forward, "a claim past the deadline never releases the statement")
	assert.Equal(t, openapi.ReviewStatusExpired, resp.Review.Status)
	assert.Equal(t, string(models.ReviewStatusExpired), readStoredReview(t, first.Review.ID).Status)
}

// An approval that checked its deadline in time commits after the claim read
// the review as lapsed. The claim must release it, not answer it as final.
func testClaimAfterALateApprovalReleases(t *testing.T) {
	sc := seedReviewingSidecar(t, "late-approval")
	setRuleTTLs(t, ptr.Int(900), ptr.Int(600))
	first := fileReview(t, sc, uniqueStatement())
	pastDeadline(t, first.Review.ID)

	const hook = "test:approval-races-the-claim"
	fired := false
	require.NoError(t, models.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if fired || !strings.Contains(tx.Statement.SQL.String(), "FROM private.reviews rv") {
			return
		}
		fired = true
		require.NoError(t, models.DB.Exec(`UPDATE private.reviews SET status = ?, expires_at = ? WHERE id = ?`,
			models.ReviewStatusApproved, time.Now().UTC().Add(10*time.Minute), first.Review.ID).Error)
	}))
	t.Cleanup(func() { _ = models.DB.Callback().Query().Remove(hook) })

	rec := claimSidecarReview(sc, first.Review.ID)
	require.True(t, fired, "the approval did not land after the claim's read")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	assert.True(t, resp.Forward, "a live approval must release the statement")
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), resp.Review.Status)
	assert.Equal(t, string(models.ReviewStatusExecuted), readStoredReview(t, first.Review.ID).Status)
}

func testGetReviewReportsExpiryWithoutWriting(t *testing.T) {
	sc := seedStatusSidecar(t, "expiry-reader")

	for _, status := range []models.ReviewStatusType{models.ReviewStatusPending, models.ReviewStatusApproved} {
		t.Run(string(status), func(t *testing.T) {
			rev := seedStatusReview(t, sc, status)
			deadline := pastDeadline(t, rev.ID)
			before := reviewSnapshot(t, rev)

			for range 2 {
				rec := getReview(sc, rev.ID)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var got openapi.SidecarReviewStatus
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, openapi.ReviewStatusExpired, got.Status)
				if assert.NotNil(t, got.ExpiresAt) {
					assert.True(t, deadline.Equal(*got.ExpiresAt), "want %v, got %v", deadline, *got.ExpiresAt)
				}
			}
			assert.JSONEq(t, before, reviewSnapshot(t, rev), "a status read never records the expiry")
		})
	}

	t.Run("a review inside its deadline", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusPending)
		deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		require.NoError(t, models.DB.Exec(`UPDATE private.reviews SET expires_at = ? WHERE id = ?`,
			deadline, rev.ID).Error)

		rec := getReview(sc, rev.ID)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got openapi.SidecarReviewStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), got.Status)
		if assert.NotNil(t, got.ExpiresAt) {
			assert.True(t, deadline.Equal(*got.ExpiresAt))
		}
	})
}

func testPutReviewRefusesAnExpiredReview(t *testing.T) {
	sc := seedStatusSidecar(t, "expired-decider")

	for _, tc := range []struct {
		name   string
		status models.ReviewStatusType
		body   string
		groups []string
	}{
		{"a reviewer approves a pending review", models.ReviewStatusPending, `{"status":"approved"}`, []string{"dba"}},
		{"a reviewer rejects a pending review", models.ReviewStatusPending, `{"status":"rejected","rejection_reason":"late"}`, []string{"dba"}},
		{"a reviewer revokes an approved review", models.ReviewStatusApproved, `{"status":"revoked"}`, []string{"dba"}},
		{"an admin approves a pending review", models.ReviewStatusPending, `{"status":"approved"}`, []string{types.GroupAdmin}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := seedStatusReview(t, sc, tc.status)
			pastDeadline(t, rev.ID)

			rec := putReview(t, rev.ID, tc.body, tc.groups)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"message":"review expired"}`, rec.Body.String())

			stored := readStoredReview(t, rev.ID)
			assert.Equal(t, string(models.ReviewStatusExpired), stored.Status, "an eligible caller records the expiry")
			assert.False(t, stored.StatementHash.Valid)
			assert.Equal(t, "done", stored.SessionStatus)

			got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, rev.ID)
			require.NoError(t, err)
			require.Len(t, got.ReviewGroups, 1, "the refused decision writes no group row")
			assert.Equal(t, rev.ReviewGroups[0].Status, got.ReviewGroups[0].Status)
			assert.Nil(t, got.RejectionReason)
		})
	}

	t.Run("an outsider changes nothing", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusPending)
		pastDeadline(t, rev.ID)
		before := reviewSnapshot(t, rev)

		// It cannot see the review, so the review does not exist for it.
		rec := putReview(t, rev.ID, `{"status":"approved"}`, []string{"engineering"})
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"resource not found"}`, rec.Body.String())
		assert.JSONEq(t, before, reviewSnapshot(t, rev))
		assert.Equal(t, string(models.ReviewStatusPending), readStoredReview(t, rev.ID).Status)
	})

	t.Run("an unknown status is checked first", func(t *testing.T) {
		rev := seedStatusReview(t, sc, models.ReviewStatusPending)
		pastDeadline(t, rev.ID)
		before := reviewSnapshot(t, rev)

		rec := putReview(t, rev.ID, `{"status":"foo"}`, []string{types.GroupAdmin})
		assert.NotContains(t, rec.Body.String(), "review expired")
		assert.Contains(t, rec.Body.String(), "unknown status")
		assert.JSONEq(t, before, reviewSnapshot(t, rev))
	})
}

func testPutReviewAfterTheRowExpired(t *testing.T) {
	sc := seedStatusSidecar(t, "recorded-expiry")
	rev := seedStatusReview(t, sc, models.ReviewStatusPending)
	pastDeadline(t, rev.ID)
	expired, status, err := models.ExpireSidecarReview(models.DB, statusTestOrgID, rev.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, expired)
	require.Equal(t, models.ReviewStatusExpired, status)
	before := reviewSnapshot(t, rev)

	rec := putReview(t, rev.ID, `{"status":"approved"}`, []string{"dba"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"message":"review expired"}`, rec.Body.String())
	assert.JSONEq(t, before, reviewSnapshot(t, rev))
}

func testPutReviewStartsTheApprovalClock(t *testing.T) {
	sc := seedReviewingSidecar(t, "approval-clock")

	t.Run("an approval limit", func(t *testing.T) {
		setRuleTTLs(t, ptr.Int(900), ptr.Int(600))
		first := fileReview(t, sc, uniqueStatement())
		// A rule edit after filing moves nothing: the review copied the limits.
		setRuleTTLs(t, nil, ptr.Int(3600))

		body := approveAsDBA(t, first.Review.ID)
		want := time.Now().UTC().Add(600 * time.Second)
		if assert.NotNil(t, body.ExpiresAt, "the answer shows the approval deadline") {
			assert.WithinDuration(t, want, *body.ExpiresAt, 2*time.Second)
		}
		if assert.NotNil(t, body.ApprovalTTLSec) {
			assert.Equal(t, 600, *body.ApprovalTTLSec)
		}
		stored := readStoredReview(t, first.Review.ID)
		if assert.NotNil(t, stored.ExpiresAt) {
			assert.WithinDuration(t, want, *stored.ExpiresAt, 2*time.Second)
		}
	})

	t.Run("no approval limit clears the deadline", func(t *testing.T) {
		setRuleTTLs(t, ptr.Int(900), nil)
		first := fileReview(t, sc, uniqueStatement())
		require.NotNil(t, first.Review.ExpiresAt)

		rec := putReview(t, first.Review.ID, `{"status":"approved"}`, []string{"dba"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "expires_at")
		assert.Nil(t, readStoredReview(t, first.Review.ID).ExpiresAt, "the pending deadline ends at the approval")
	})
}

// With no limit on the rule, nothing a sidecar reads carries the new keys, and
// an old approval stays claimable.
func testReviewsWithoutTTLKeepTodaysAnswers(t *testing.T) {
	sc := seedReviewingSidecar(t, "no-limit")
	raw := uniqueStatement()
	newKeys := []string{"expires_at", "approval_ttl_sec"}

	rec := postReview(sc, raw)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	first := decodeReviewResponse(t, rec)
	for _, key := range newKeys {
		assert.NotContains(t, rec.Body.String(), key)
	}

	put := putReview(t, first.Review.ID, `{"status":"approved"}`, []string{"dba"})
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	get := getReview(sc, first.Review.ID)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	for _, key := range newKeys {
		assert.NotContains(t, put.Body.String(), key)
		assert.NotContains(t, get.Body.String(), key)
	}

	require.NoError(t, models.DB.Exec(
		`UPDATE private.reviews SET created_at = created_at - interval '1 day' WHERE id = ?`, first.Review.ID).Error)
	rec = postReview(sc, raw)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReviewResponse(t, rec)
	assert.True(t, resp.Forward, "an approval with no limit never lapses")
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), resp.Review.Status)
	for _, key := range newKeys {
		assert.NotContains(t, rec.Body.String(), key)
	}
}
