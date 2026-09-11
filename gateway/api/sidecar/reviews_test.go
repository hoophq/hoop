package apisidecar

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

// The handler refuses outside the control plane, so a test reaching past that
// guard has to run as one. appconfig.Load is one-shot (it returns early once
// loaded), so a test binary gets a single mode and the gateway-mode refusal is
// covered by booting a real gateway rather than from here.
func TestMain(m *testing.M) {
	if err := appconfig.Load(appconfig.AppModeControlPlane); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// The whole approval policy lives in these rows. EVL-273 put none of it in
// code, so if this drifts a sidecar review silently becomes unapprovable by
// one of the two roles, and nothing says so at the time.
func TestEligibleReviewGroups(t *testing.T) {
	groups := eligibleReviewGroups("org-1")

	names := make([]string, 0, len(groups))
	for _, rg := range groups {
		names = append(names, rg.GroupName)
		assert.Equal(t, models.ReviewStatusPending, rg.Status)
		assert.Equal(t, "org-1", rg.OrgID)
		assert.NotEmpty(t, rg.ID, "a group row needs its own id: CreateReview inserts it verbatim")
	}

	assert.ElementsMatch(t, []string{types.GroupAdmin, types.GroupApprover}, names,
		"an admin or an approver settles a sidecar review, so both need a row")
}

// The admin group name is read from the environment, so spelling it would
// produce a row nobody is in for an organization that renamed it.
func TestEligibleReviewGroupsDoesNotHardcodeAdmin(t *testing.T) {
	for _, rg := range eligibleReviewGroups("org-1") {
		if rg.GroupName == types.GroupApprover {
			continue
		}
		assert.Equal(t, types.GroupAdmin, rg.GroupName)
	}
}

// A sidecar review has no connection. Sending the fields empty would invite a
// client to read meaning into an empty string, so they are absent instead.
func TestToOpenApiSidecarReviewOmitsConnectionFields(t *testing.T) {
	rev := &models.Review{
		ID:           "review-1",
		SessionID:    "session-1",
		Type:         models.ReviewTypeOneTime,
		Status:       models.ReviewStatusPending,
		SidecarID:    sql.NullString{String: "sidecar-1", Valid: true},
		ListenerName: sql.NullString{String: "appdb", Valid: true},
		ReviewGroups: eligibleReviewGroups("org-1"),
	}

	got := toOpenApiSidecarReview(rev)

	assert.Equal(t, "review-1", got.ID)
	assert.Equal(t, "session-1", got.Session)
	assert.Equal(t, "sidecar-1", *got.SidecarID)
	assert.Equal(t, "appdb", *got.ListenerName)
	assert.Len(t, got.ReviewGroupsData, 2)
	assert.Equal(t, models.ReviewStatusPending.Str(), string(got.Status))

	// openapi.Review carries no connection field at all, so the absence is
	// structural. This pins the response the sidecar actually reads.
	assert.Nil(t, got.RevokeAt, "a sidecar review is granted no access window")
	assert.Nil(t, got.TimeWindow)
	assert.Nil(t, got.AccessRequestRuleName, "the policy is fixed, not carried by a rule")
}

// Without the middleware there is no sidecar, and the handler must say so
// rather than read a nil row: the difference between 401 and a panic.
func TestPostReviewRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews",
		strings.NewReader(`{"listener_name":"appdb","payload":"c2VsZWN0IDE7"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PostReview(c)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "access denied")
}

// A statement that is not valid base64 never reaches the database, so this
// refuses before any write. The reviewer would otherwise be shown whatever
// arrived, because nothing further down this path decodes anything.
func TestPostReviewRefusesAPayloadThatIsNotBase64(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	// The same key SidecarAuthMiddleware sets, so the handler gets past its
	// first guard without a middleware or a database.
	c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews",
		strings.NewReader(`{"listener_name":"appdb","payload":"!!!not-base64!!!"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PostReview(c)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not valid base64")
}
