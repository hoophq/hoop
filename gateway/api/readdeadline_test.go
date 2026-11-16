package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The /api middleware wraps the response writer. A handler under it must
// still reach the connection: POST /sidecars/events sets a read deadline.
func TestAPIRouterHandlersCanSetAReadDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &Api{}
	route := a.newEngine()
	r := a.newAPIRouter(route, "")
	r.POST("/read-deadline", func(c *gin.Context) {
		if err := http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusNoContent)
	})
	srv := httptest.NewServer(route)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Post(srv.URL+"/api/read-deadline", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}
