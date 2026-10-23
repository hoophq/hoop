package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each review route is served under its approval path with the same handler.
func TestApprovalRoutesAliasReviewRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	(&Api{}).buildRoutes(apiroutes.New(engine.Group("/api")))

	routes := map[string]string{}
	for _, ri := range engine.Routes() {
		routes[ri.Method+" "+ri.Path] = ri.Handler
	}
	for _, tt := range []struct{ method, review, approval string }{
		{http.MethodGet, "/api/reviews", "/api/approvals"},
		{http.MethodGet, "/api/reviews/count", "/api/approvals/count"},
		{http.MethodGet, "/api/reviews/:id", "/api/approvals/:id"},
		{http.MethodPut, "/api/reviews/:id", "/api/approvals/:id"},
		{http.MethodPut, "/api/sessions/:session_id/review", "/api/sessions/:session_id/approval"},
	} {
		review, ok := routes[tt.method+" "+tt.review]
		require.True(t, ok, "%s %s is not registered", tt.method, tt.review)
		approval, ok := routes[tt.method+" "+tt.approval]
		require.True(t, ok, "%s %s is not registered", tt.method, tt.approval)
		assert.Equal(t, review, approval, "%s %s and %s run different handlers", tt.method, tt.review, tt.approval)
	}
}
