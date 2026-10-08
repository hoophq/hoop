package reviewapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedGatewayReview(t *testing.T, ownerID string, groups ...string) *models.Review {
	t.Helper()
	rev := newGatewayReview(ownerID, groups...)
	require.NoError(t, models.CreateReview(rev, ""))
	return rev
}

func newGatewayReview(ownerID string, groups ...string) *models.Review {
	rev := &models.Review{
		ID:             uuid.NewString(),
		OrgID:          decisionTestOrgID,
		Type:           models.ReviewTypeOneTime,
		Status:         models.ReviewStatusPending,
		SessionID:      uuid.NewString(),
		ConnectionName: "postgres-demo",
		OwnerID:        ownerID,
		OwnerEmail:     ownerID + "@hoop.dev",
		CreatedAt:      time.Now().UTC(),
	}
	for _, g := range groups {
		rev.ReviewGroups = append(rev.ReviewGroups, models.ReviewGroups{
			ID: uuid.NewString(), OrgID: decisionTestOrgID, GroupName: g, Status: models.ReviewStatusPending})
	}
	return rev
}

func seedForceApproveConnection(t *testing.T, name string, forceGroups ...string) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, ?, 'custom', 'redis')`, decisionTestOrgID, name).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, force_approve_groups)
		VALUES (?, ?, 'custom', 'redis', ?, ?)`, decisionTestOrgID, name, name, pq.StringArray(forceGroups)).Error)
}

func serveReviews(userID string, groups []string, id string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/reviews", nil)
	c.Set(storagev2.ContextKey, &storagev2.Context{APIContext: &types.APIContext{
		OrgID: decisionTestOrgID, UserID: userID, UserGroups: groups}})
	h := NewHandler(nil)
	if id == "" {
		h.List(c)
		return w
	}
	c.Params = gin.Params{{Key: "id", Value: id}}
	h.GetByIdOrSid(c)
	return w
}

func listedReviewIDs(t *testing.T, userID string, groups []string) []string {
	t.Helper()
	w := serveReviews(userID, groups, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got []openapi.Review
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestReviewVisibilityIgnoresTheRowAddedOnDenial(t *testing.T) {
	startDecisionTestDB(t)
	rev := seedGatewayReview(t, "user-requester", "dba")

	requester := newFakeContext("user-requester", "requester@hoop.dev", []string{"finance"})
	requester.OrgID = decisionTestOrgID
	stored, err := models.GetReviewByIdOrSid(decisionTestOrgID, rev.ID)
	require.NoError(t, err)
	decided, err := doIndividualReview(requester, stored, &models.Connection{}, models.ReviewStatusRejected)
	require.NoError(t, err)
	require.NoError(t, persistDecision(decided, models.ReviewStatusPending))

	got, err := models.GetReviewByIdOrSid(decisionTestOrgID, rev.ID)
	require.NoError(t, err)
	var recorded bool
	for _, rg := range got.ReviewGroups {
		recorded = recorded || (rg.GroupName == "finance" && rg.AddedOnDenial)
	}
	require.True(t, recorded, "the denial is recorded under the requester's group")

	assert.Empty(t, listedReviewIDs(t, "user-peer", []string{"finance"}))
	assert.Equal(t, http.StatusNotFound, serveReviews("user-peer", []string{"finance"}, rev.ID).Code)
	assert.Equal(t, []string{rev.ID}, listedReviewIDs(t, "user-requester", []string{"finance"}))
	assert.Equal(t, []string{rev.ID}, listedReviewIDs(t, "user-dba", []string{"dba"}))
}

func TestReviewVisibility(t *testing.T) {
	startDecisionTestDB(t)

	requested := seedGatewayReview(t, "user-requester", "dba")
	other := seedGatewayReview(t, "user-other", "sre")
	sidecarRev := seedApprovedSidecarReview(t)
	sidecarID := sidecarRev.OwnerID
	all := []*models.Review{requested, other, sidecarRev}

	for _, tt := range []struct {
		name    string
		userID  string
		groups  []string
		visible []*models.Review
	}{
		{"the requester sees the review they requested", "user-requester", []string{"finance"}, []*models.Review{requested}},
		{"a reviewer group member sees gateway and sidecar reviews", "user-dba", []string{"dba"}, []*models.Review{requested, sidecarRev}},
		{"a user outside the review sees none", "user-outsider", nil, nil},
		{"an admin sees every review", "user-admin", []string{types.GroupAdmin}, all},
		{"an auditor sees every review", "user-auditor", []string{types.GroupAuditor}, all},
		{"a sidecar review has no requester", sidecarID, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertVisibleReviews(t, all, tt.userID, tt.groups, tt.visible)
		})
	}
}

func TestReviewVisibilityForceApprovers(t *testing.T) {
	startDecisionTestDB(t)
	seedForceApproveConnection(t, "pg-ruled", "oncall")
	seedForceApproveConnection(t, "pg-plain", "oncall")

	ruled := newGatewayReview("user-requester", "dba")
	ruled.ConnectionName = "pg-ruled"
	ruled.AccessRequestRuleName = ptr.String("prod-writes")
	ruled.ForceApprovalGroups = pq.StringArray{"sre"}
	require.NoError(t, models.CreateReview(ruled, ""))

	plain := newGatewayReview("user-requester", "dba")
	plain.ConnectionName = "pg-plain"
	require.NoError(t, models.CreateReview(plain, ""))

	sidecarRev := seedApprovedSidecarReview(t, func(r *models.Review) {
		r.ForceApprovalGroups = pq.StringArray{"security"}
	})
	sidecarOnMirror := seedApprovedSidecarReview(t, func(r *models.Review) {
		r.ConnectionName = "pg-plain"
	})
	all := []*models.Review{ruled, plain, sidecarRev, sidecarOnMirror}

	for _, tt := range []struct {
		name    string
		groups  []string
		visible []*models.Review
	}{
		{"a rule's force group sees the review", []string{"sre"}, []*models.Review{ruled}},
		{"a connection's force group sees only reviews without rule force groups", []string{"oncall"}, []*models.Review{plain}},
		{"a sidecar rule's force group sees the review", []string{"security"}, []*models.Review{sidecarRev}},
		{"a user outside every force group sees none", []string{"finance"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertVisibleReviews(t, all, "user-"+tt.groups[0], tt.groups, tt.visible)
			for _, r := range all {
				assert.Equal(t, slices.Contains(tt.visible, r), mayForceApprove(t, r, tt.groups),
					"visibility and doForcedReview disagree on review %s", r.ID)
			}
		})
	}
}

// mayForceApprove runs doForcedReview with the connection DoReview loads. It writes nothing.
func mayForceApprove(t *testing.T, r *models.Review, groups []string) bool {
	t.Helper()
	rev, err := models.GetReviewByIdOrSid(decisionTestOrgID, r.ID)
	require.NoError(t, err)
	var conn *models.Connection
	if !IsSidecarReview(rev) {
		conn, err = models.GetConnectionByNameOrID(models.NewAdminContext(decisionTestOrgID), rev.ConnectionName)
		require.NoError(t, err)
		require.NotNil(t, conn)
	}
	ctx := newFakeContext("user-"+groups[0], groups[0]+"@hoop.dev", groups)
	ctx.OrgID = decisionTestOrgID
	_, err = doForcedReview(ctx, rev, conn, models.ReviewStatusApproved)
	if errors.Is(err, ErrNotEligible) {
		return false
	}
	require.NoError(t, err)
	return true
}

func assertVisibleReviews(t *testing.T, all []*models.Review, userID string, groups []string, visible []*models.Review) {
	t.Helper()
	want := []string{}
	for _, r := range visible {
		want = append(want, r.ID)
	}
	assert.ElementsMatch(t, want, listedReviewIDs(t, userID, groups))

	for _, r := range all {
		wantCode := http.StatusNotFound
		for _, v := range visible {
			if v.ID == r.ID {
				wantCode = http.StatusOK
			}
		}
		assert.Equal(t, wantCode, serveReviews(userID, groups, r.ID).Code, "get by id %s", r.ID)
		assert.Equal(t, wantCode, serveReviews(userID, groups, r.SessionID).Code, "get by session %s", r.SessionID)
	}
}
