package reviewapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedGatewayReview(t *testing.T, ownerID string, groups ...string) *models.Review {
	t.Helper()
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
	require.NoError(t, models.CreateReview(rev, ""))
	return rev
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
	require.NoError(t, persistDecision(decided, &models.Connection{}, models.ReviewStatusPending))

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
			want := []string{}
			for _, r := range tt.visible {
				want = append(want, r.ID)
			}
			assert.ElementsMatch(t, want, listedReviewIDs(t, tt.userID, tt.groups))

			for _, r := range all {
				wantCode := http.StatusNotFound
				for _, v := range tt.visible {
					if v.ID == r.ID {
						wantCode = http.StatusOK
					}
				}
				assert.Equal(t, wantCode, serveReviews(tt.userID, tt.groups, r.ID).Code, "get by id %s", r.ID)
				assert.Equal(t, wantCode, serveReviews(tt.userID, tt.groups, r.SessionID).Code, "get by session %s", r.SessionID)
			}
		})
	}
}
