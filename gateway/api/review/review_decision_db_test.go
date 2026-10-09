package reviewapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decide runs PUT /reviews/:id, or PUT /sessions/:session_id/review when
// param is "session_id", as the user.
func decide(userID string, groups []string, param, id, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/reviews/"+id, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, &storagev2.Context{APIContext: &types.APIContext{
		OrgID: decisionTestOrgID, UserID: userID, UserEmail: userID + "@hoop.dev", UserGroups: groups}})
	c.Params = gin.Params{{Key: param, Value: id}}
	NewHandler(func(_, _, _, _, _, _ string) {}).ReviewByIdOrSid(c)
	return w
}

// A decision answers 404 where GET does, so it never tells that a hidden
// review exists. A user who can see the review gets the answer of today.
func TestReviewDecisionAnswers404WhereTheReadDoes(t *testing.T) {
	startDecisionTestDB(t)
	seedForceApproveConnection(t, "pg-ruled", "oncall")
	seedForceApproveConnection(t, "pg-plain", "oncall")
	const (
		approve = `{"status":"APPROVED"}`
		force   = `{"status":"APPROVED","force_review":true}`
		revoke  = `{"status":"REVOKED"}`
	)
	plain := func(t *testing.T) *models.Review {
		rev := newGatewayReview("user-requester", "dba")
		rev.ConnectionName = "pg-plain"
		require.NoError(t, models.CreateReview(rev, ""))
		return rev
	}
	ruled := func(t *testing.T) *models.Review {
		rev := newGatewayReview("user-requester", "dba")
		rev.ConnectionName = "pg-ruled"
		rev.AccessRequestRuleName = ptr.String("prod-writes")
		rev.ForceApprovalGroups = pq.StringArray{"sre"}
		require.NoError(t, models.CreateReview(rev, ""))
		return rev
	}
	sidecar := func(t *testing.T) *models.Review { return seedApprovedSidecarReview(t) }

	for _, tt := range []struct {
		name   string
		review func(*testing.T) *models.Review
		groups []string
		body   string
		want   int
	}{
		{"an outsider approves", plain, []string{"finance"}, approve, http.StatusNotFound},
		{"an outsider forces", ruled, []string{"finance"}, force, http.StatusNotFound},
		{"an outsider revokes a sidecar review", sidecar, []string{"finance"}, revoke, http.StatusNotFound},
		{"a group that is not a reviewer of this review", plain, []string{"sre"}, approve, http.StatusNotFound},
		{"a reviewer approves", plain, []string{"dba"}, approve, http.StatusOK},
		{"a reviewer revokes a sidecar review", sidecar, []string{"dba"}, revoke, http.StatusOK},
		{"a rule's force approver forces", ruled, []string{"sre"}, force, http.StatusOK},
		{"a connection's force approver forces", plain, []string{"oncall"}, force, http.StatusOK},
		{"a force approver who does not force is not eligible", ruled, []string{"sre"}, approve, http.StatusBadRequest},
		{"an auditor outside the groups is not eligible", plain, []string{types.GroupAuditor}, approve, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rev := tt.review(t)
			userID := "user-" + tt.groups[0]
			read := serveReviews(userID, tt.groups, rev.ID).Code
			w := decide(userID, tt.groups, "id", rev.ID, tt.body)
			assert.Equal(t, tt.want, w.Code, "body: %s", w.Body)
			assert.Equal(t, read == http.StatusNotFound, w.Code == http.StatusNotFound,
				"GET answered %d, PUT answered %d", read, w.Code)
		})
	}

	t.Run("the requester cannot approve their own review", func(t *testing.T) {
		rev := plain(t)
		w := decide("user-requester", []string{"finance"}, "id", rev.ID, approve)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body)
		assert.Contains(t, w.Body.String(), ErrSelfApproval.Error())
	})

	t.Run("a hidden review reads as one that does not exist", func(t *testing.T) {
		rev := plain(t)
		hidden := decide("user-outsider", []string{"finance"}, "id", rev.ID, approve)
		missing := decide("user-outsider", []string{"finance"}, "id", uuid.NewString(), approve)
		require.Equal(t, http.StatusNotFound, missing.Code, "body: %s", missing.Body)
		assert.Equal(t, missing.Body.String(), hidden.Body.String())
		assert.Equal(t, serveReviews("user-outsider", []string{"finance"}, rev.ID).Body.String(), hidden.Body.String())
	})

	t.Run("by session id", func(t *testing.T) {
		rev := plain(t)
		assert.Equal(t, http.StatusNotFound, decide("user-outsider", []string{"finance"}, "session_id", rev.SessionID, approve).Code)
		w := decide("user-dba", []string{"dba"}, "session_id", rev.SessionID, approve)
		assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	})
}
