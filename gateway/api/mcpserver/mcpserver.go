package mcpserver

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sends MCP-level ping requests at this interval to keep idle sessions warm.
// Without this, long-running tool handlers (e.g. reviews_wait) sit silently on
// the wire long enough that intermediate layers tear down the connection.
const mcpKeepAliveInterval = 30 * time.Second

type MCPServer struct {
	handler *mcp.StreamableHTTPHandler
}

func New(releaseConnFn reviewapi.TransportReleaseConnectionFunc) *MCPServer {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "hoop",
		Version: version.Get().Version,
	}, &mcp.ServerOptions{
		KeepAlive: mcpKeepAliveInterval,
	})

	registerConnectionTools(server)
	registerGuardrailTools(server)
	registerDataMaskingTools(server)
	registerUserGroupTools(server)
	registerUserTools(server)
	registerReviewTools(server, releaseConnFn)
	registerAccessRequestRuleTools(server)
	registerRunbookRuleTools(server)
	registerSessionTools(server)
	registerServerInfoTools(server)
	registerMeTools(server)
	registerExecTools(server)
	registerSchemaTools(server)

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		nil,
	)

	return &MCPServer{handler: handler}
}

// GinHandler bridges Gin auth context (storage context + access token) into
// the MCP request context, then delegates to StreamableHTTPHandler.
func (m *MCPServer) GinHandler(c *gin.Context) {
	sc := storagev2.ParseContext(c)
	token := apiroutes.GetAccessTokenFromRequest(c)
	ctx := withStorageContext(c.Request.Context(), sc)
	ctx = withAccessToken(ctx, token)
	req := c.Request.WithContext(ctx)
	m.handler.ServeHTTP(c.Writer, req)
}
