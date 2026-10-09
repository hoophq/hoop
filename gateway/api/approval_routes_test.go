package api

import (
	"net/http"
	"net/http/httptest"
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

// The alias runs the whole handler chain, middleware included.
func TestWithApprovalAliasSharesTheChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	middleware := func(c *gin.Context) { c.Header("X-Middleware", "ran"); c.Next() }
	handler := func(c *gin.Context) { c.String(http.StatusOK, "id="+c.Param("id")) }
	withApprovalAlias(engine.GET, "/reviews/:id", middleware, handler)

	for _, path := range []string{"/reviews/abc", "/approvals/abc"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, w.Code, path)
		assert.Equal(t, "id=abc", w.Body.String(), path)
		assert.Equal(t, "ran", w.Header().Get("X-Middleware"), path)
	}
	assert.Panics(t, func() { withApprovalAlias(engine.GET, "/no-alias", handler) })
}
