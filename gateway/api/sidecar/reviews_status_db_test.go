package apisidecar

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const statusTestOrgID = "00000000-0000-0000-0000-0000000000b1"

func startStatusTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-review-status-test')`, statusTestOrgID).Error)
}

func seedStatusSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     statusTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

// seedStatusReview files a review in the given status. A decided review has a
// decided group, the way an approval or rejection leaves it.
func seedStatusReview(t *testing.T, sc *models.Sidecar, status models.ReviewStatusType) *models.Review {
	t.Helper()
	sessionID := uuid.NewString()
	statement := "DELETE FROM users WHERE id = '" + sessionID + "';"
	rev := &models.Review{
		ID:                    uuid.NewString(),
		OrgID:                 statusTestOrgID,
		Type:                  models.ReviewTypeOneTime,
		Status:                models.ReviewStatusPending,
		SessionID:             sessionID,
		SidecarID:             sql.NullString{String: sc.ID, Valid: true},
		ListenerName:          sql.NullString{String: "appdb", Valid: true},
		StatementHash:         sql.NullString{String: models.HashStatement([]byte(statement)), Valid: true},
		OwnerID:               sc.ID,
		OwnerEmail:            reviewOwnerEmail,
		AccessRequestRuleName: ptr.String("payments-approvers"),
		CreatedAt:             time.Now().UTC(),
	}
	groupStatus := models.ReviewStatusPending
	var reviewedAt *time.Time
	if status != models.ReviewStatusPending {
		groupStatus = models.ReviewStatusApproved
		if status == models.ReviewStatusRejected {
			groupStatus = models.ReviewStatusRejected
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		reviewedAt = &now
	}
	rev.ReviewGroups = []models.ReviewGroups{{
		ID:         uuid.NewString(),
		OrgID:      statusTestOrgID,
		ReviewID:   rev.ID,
		GroupName:  "dba",
		Status:     groupStatus,
		OwnerEmail: ptr.String("reviewer@example.com"),
		ReviewedAt: reviewedAt,
	}}
	sess := models.Session{
		ID:             sessionID,
		OrgID:          statusTestOrgID,
		BlobInput:      models.BlobInputType(statement),
		ConnectionType: reviewConnectionType,
		Verb:           "exec",
		Status:         "open",
		UserID:         sc.ID,
		UserName:       sc.Name,
		UserEmail:      reviewOwnerEmail,
		CreatedAt:      time.Now().UTC(),
	}
	_, err := models.CreateSidecarReview(models.DB, sess, rev, statement)
	require.NoError(t, err)
	if status != models.ReviewStatusPending {
		require.NoError(t, models.UpdateReviewStatus(statusTestOrgID, rev.ID, status))
	}
	return rev
}

// reviewSnapshot is every row a claim writes: the review, its groups and its
// session.
func reviewSnapshot(t *testing.T, rev *models.Review) string {
	t.Helper()
	var snap string
	require.NoError(t, models.DB.Raw(`
	SELECT json_build_array(
		(SELECT row_to_json(r) FROM private.reviews r WHERE r.id = ?),
		(SELECT json_agg(rg ORDER BY rg.id) FROM private.review_groups rg WHERE rg.review_id = ?),
		(SELECT row_to_json(s) FROM private.sessions s WHERE s.id = ?)
	)::text`, rev.ID, rev.ID, rev.SessionID).Scan(&snap).Error)
	return snap
}

func getReview(sc *models.Sidecar, reviewID string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	c.Params = gin.Params{{Key: "id", Value: reviewID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars/reviews/"+reviewID, nil)
	GetReview(c)
	return rec
}

func TestGetReviewNeverChangesTheReview(t *testing.T) {
	startStatusTestDB(t)
	sc := seedStatusSidecar(t, "status-reader")

	for _, status := range []models.ReviewStatusType{
		models.ReviewStatusPending,
		models.ReviewStatusApproved,
		models.ReviewStatusRejected,
		models.ReviewStatusRevoked,
		models.ReviewStatusProcessing,
		models.ReviewStatusExecuted,
		models.ReviewStatusUnknown,
	} {
		t.Run(string(status), func(t *testing.T) {
			rev := seedStatusReview(t, sc, status)
			before := reviewSnapshot(t, rev)

			// Twice: a second read must not see a spent approval.
			for range 2 {
				rec := getReview(sc, rev.ID)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

				var got openapi.SidecarReviewStatus
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, rev.ID, got.ID)
				assert.Equal(t, string(status), string(got.Status))
				assert.Equal(t, "appdb", got.ListenerName)
				assert.Equal(t, "payments-approvers", got.ApprovalRule)
				assert.Equal(t, status == models.ReviewStatusPending, got.DecidedAt == nil)
				assert.NotContains(t, rec.Body.String(), "DELETE FROM")
				assert.NotContains(t, rec.Body.String(), "reviewer@example.com")
			}

			assert.JSONEq(t, before, reviewSnapshot(t, rev))
		})
	}
}

func TestGetReviewAnswersNotFound(t *testing.T) {
	startStatusTestDB(t)
	owner := seedStatusSidecar(t, "owner")
	other := seedStatusSidecar(t, "other")
	rev := seedStatusReview(t, owner, models.ReviewStatusApproved)

	for name, tc := range map[string]struct {
		sidecar  *models.Sidecar
		reviewID string
	}{
		"unknown id":      {owner, uuid.NewString()},
		"malformed uuid":  {owner, "not-a-uuid"},
		"foreign sidecar": {other, rev.ID},
	} {
		t.Run(name, func(t *testing.T) {
			rec := getReview(tc.sidecar, tc.reviewID)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func listReviews(sc *models.Sidecar, query string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars/reviews"+query, nil)
	ListReviews(c)
	return rec
}

func TestListReviewsListsOnlyThisSidecarsReviews(t *testing.T) {
	startStatusTestDB(t)
	owner := seedStatusSidecar(t, "lister")
	other := seedStatusSidecar(t, "neighbour")
	pending := seedStatusReview(t, owner, models.ReviewStatusPending)
	rejected := seedStatusReview(t, owner, models.ReviewStatusRejected)
	foreign := seedStatusReview(t, other, models.ReviewStatusPending)
	before := reviewSnapshot(t, pending)

	ids := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got []openapi.SidecarReviewStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		out := []string{}
		for _, r := range got {
			out = append(out, r.ID)
		}
		assert.NotContains(t, rec.Body.String(), "DELETE FROM")
		assert.NotContains(t, rec.Body.String(), "reviewer@example.com")
		return out
	}

	all := ids(listReviews(owner, ""))
	assert.ElementsMatch(t, []string{pending.ID, rejected.ID}, all)
	assert.NotContains(t, all, foreign.ID)
	assert.Equal(t, []string{pending.ID}, ids(listReviews(owner, "?status=pending")))
	assert.Len(t, ids(listReviews(owner, "?limit=1")), 1)
	assert.JSONEq(t, before, reviewSnapshot(t, pending))

	empty := listReviews(seedStatusSidecar(t, "quiet"), "")
	assert.JSONEq(t, `[]`, empty.Body.String())
}

func TestListReviewsRefusesABadQuery(t *testing.T) {
	startStatusTestDB(t)
	sc := seedStatusSidecar(t, "bad-query")
	for _, q := range []string{"?status=DONE", "?limit=0", "?limit=201", "?limit=ten"} {
		t.Run(q, func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, listReviews(sc, q).Code)
		})
	}
}
