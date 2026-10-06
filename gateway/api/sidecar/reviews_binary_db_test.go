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
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedReviewingSidecar stores a sidecar whose appdb listener files reviews
// under payments-approvers, and that rule.
func seedReviewingSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     statusTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name:     "appdb",
			Analyzer: &daemon.LaneAnalyzerConfig{ApprovalRule: "payments-approvers"},
		}}},
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	seedApprovalRule(t)
	return sc
}

func postReview(sc *models.Sidecar, raw []byte) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", sc)
	body, _ := json.Marshal(map[string]string{
		"listener_name": "appdb",
		"approval_rule": "payments-approvers",
		"payload":       base64.StdEncoding.EncodeToString(raw),
	})
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	PostReview(c)
	return rec
}

// storedInputs reads back both blobs a filed review wrote.
func storedInputs(t *testing.T, resp openapi.SidecarReviewResponse) (session, review string) {
	t.Helper()
	sess, err := models.GetSessionByID(statusTestOrgID, resp.Review.Session)
	require.NoError(t, err)
	in, err := sess.GetBlobInput()
	require.NoError(t, err)
	rev, err := models.GetReviewByIdOrSid(statusTestOrgID, resp.Review.ID)
	require.NoError(t, err)
	revIn, err := rev.GetBlobInput()
	require.NoError(t, err)
	return string(in), revIn
}

// kubectl sends protobuf for create, auth can-i and auth whoami. The customer
// got 500 (SQLSTATE 22P02) and a request that could never be approved.
func TestPostReviewBinaryStatement(t *testing.T) {
	startStatusTestDB(t)
	sc := seedReviewingSidecar(t, "binary")
	raw := []byte("POST /api/v1/namespaces/default/configmaps\n\nk8s\x00\n\x0f\n\x02v1\x12\tConfigMap\xff\\x00")

	// Filed once here, so each subtest can run alone.
	rec := postReview(sc, raw)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var first openapi.SidecarReviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))

	t.Run("files it", func(t *testing.T) {
		sessIn, revIn := storedInputs(t, first)
		assert.Equal(t, displayStatement(raw), sessIn)
		assert.Equal(t, displayStatement(raw), revIn)
		assert.True(t, strings.HasPrefix(revIn, notice(len(raw))+"\n"), "the reviewer is told the statement is binary")

		got, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, first.Review.ID)
		require.NoError(t, err)
		assert.Equal(t, models.HashStatement(raw), got.StatementHash.String, "the match stays on the raw bytes")
	})

	t.Run("a retry of the same bytes answers the same review", func(t *testing.T) {
		rec := postReview(sc, raw)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var again openapi.SidecarReviewResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &again))
		assert.Equal(t, first.Review.ID, again.Review.ID)
		assert.False(t, again.Forward)
	})

	t.Run("the display sent as a statement files its own review", func(t *testing.T) {
		display := []byte(displayStatement(raw))
		rec := postReview(sc, display)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var other openapi.SidecarReviewResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &other))
		assert.NotEqual(t, first.Review.ID, other.Review.ID)
		_, revIn := storedInputs(t, other)
		assert.Equal(t, displayStatement(display), revIn)
	})

	t.Run("printable text is stored as it arrived", func(t *testing.T) {
		text := []byte("SELECT 1;\r\n\tFROM t;")
		rec := postReview(sc, text)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var resp openapi.SidecarReviewResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		sessIn, revIn := storedInputs(t, resp)
		assert.Equal(t, string(text), sessIn)
		assert.Equal(t, string(text), revIn)
	})
}
