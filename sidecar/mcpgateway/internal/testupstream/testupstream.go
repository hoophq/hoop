// Package testupstream is an MCP server for the RD-379 spike: streamable-http
// on /mcp and SSE on /sse, with tools that return PII, delete things, run
// SQL, and flood the client with notifications.
package testupstream

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Message string `json:"message"`
}

type idIn struct {
	ID string `json:"id"`
}

type customer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	SSN   string `json:"ssn"`
}

type queryIn struct {
	SQL string `json:"sql"`
}

type chattyIn struct {
	Count int `json:"count"`
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// NewServer returns the MCP server. desc overrides the echo tool's
// description, for the tool-catalog checks.
func NewServer(echoDesc string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "rd379-upstream", Version: "0.0.1"}, nil)
	if echoDesc == "" {
		echoDesc = "Echo the message back."
	}
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: echoDesc},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return text(in.Message), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "customer_lookup", Description: "Look up a customer by id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, customer, error) {
			c := customer{Name: "Jane Doe", Email: "jane.doe@example.com", SSN: "123-45-6789"}
			return text(fmt.Sprintf("Customer %s: %s, email %s, SSN %s", in.ID, c.Name, c.Email, c.SSN)), c, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "delete_customer", Description: "Delete a customer by id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, any, error) {
			return text("deleted customer " + in.ID), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "run_query", Description: "Run a SQL query."},
		func(_ context.Context, _ *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, any, error) {
			return text("ran: " + in.SQL), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "chatty", Description: "Send count log notifications, then answer."},
		func(ctx context.Context, req *mcp.CallToolRequest, in chattyIn) (*mcp.CallToolResult, any, error) {
			for i := range in.Count {
				_ = req.Session.Log(ctx, &mcp.LoggingMessageParams{Level: "info", Data: fmt.Sprintf("line %d", i)})
			}
			return text(fmt.Sprintf("sent %d notifications", in.Count)), nil, nil
		})
	return s
}

// Handler serves streamable-http on /mcp and SSE on /sse.
func Handler(s *mcp.Server) http.Handler {
	mux := http.NewServeMux()
	get := func(*http.Request) *mcp.Server { return s }
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(get, nil))
	mux.Handle("/sse", mcp.NewSSEHandler(get, nil))
	return mux
}
