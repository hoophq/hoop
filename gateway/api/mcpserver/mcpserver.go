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

// mcpSessionTimeout bounds how long an *idle* MCP session is retained in the
// server's in-memory session map before it is evicted. It is an inactivity
// timer: the SDK pauses it for the duration of every in-flight request and
// only resets it once the last request for a session finishes (see
// sessionInfo.startPOST/endPOST in the go-sdk). An actively used session — or
// one parked on a long-running wait tool — therefore never expires; only a
// genuinely abandoned session is reclaimed, which keeps memory bounded on a
// long-lived gateway without ever tearing a live client out from under itself.
//
// We intentionally do NOT use ServerOptions.KeepAlive. KeepAlive sends
// server->client pings on the *standalone* SSE stream and, on the first ping
// failure, destroys the whole session. Streamable-HTTP MCP clients that don't
// hold a standalone GET stream open (Devin, Cursor) make every ping fail,
// which evicted the session ~30s after it was created and surfaced to users as
// "session terminated (404), need to re-initialize". Long-running tool calls
// are instead kept warm by emitting progress notifications on the request's
// own POST stream (see newWaitHeartbeat / waitUntil), which is the stream that
// actually needs to stay alive.
const mcpSessionTimeout = 30 * time.Minute

type MCPServer struct {
	handler *mcp.StreamableHTTPHandler
}

func New(releaseConnFn reviewapi.TransportReleaseConnectionFunc) *MCPServer {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "hoop",
		Version: version.Get().Version,
	}, nil)

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
	registerAttributeTools(server)
	registerAccessControlTools(server)

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{SessionTimeout: mcpSessionTimeout},
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
