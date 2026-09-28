package gatewaymodetest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	apisidecar "github.com/hoophq/hoop/gateway/api/sidecar"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	if err := appconfig.Load(appconfig.AppModeGateway); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// A gateway cannot settle a sidecar review, so every review route answers 412
// before it touches the database.
func TestSidecarReviewRoutesAnswerPreconditionFailed(t *testing.T) {
	const reviewID = "9f97c0de-0000-0000-0000-000000000001"
	for name, tc := range map[string]struct {
		handler gin.HandlerFunc
		method  string
		body    string
	}{
		"get":   {apisidecar.GetReview, http.MethodGet, ""},
		"claim": {apisidecar.ClaimReview, http.MethodPost, ""},
		"post":  {apisidecar.PostReview, http.MethodPost, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
			c.Params = gin.Params{{Key: "id", Value: reviewID}}
			c.Request = httptest.NewRequest(tc.method, "/api/sidecars/reviews/"+reviewID, strings.NewReader(tc.body))

			tc.handler(c)

			assert.Equal(t, http.StatusPreconditionFailed, rec.Code)
		})
	}
}
