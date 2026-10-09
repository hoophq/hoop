// Package mcp is the sidecar's MCP server (ADR-0021): read-only tools an
// agent uses to follow an approval request after a return-mode lane denied
// its statement with the approval id. Each tool is served as approval_* and,
// for agents configured before the rename, as the review_* alias.
//
// It carries status only. The statement still runs through the lane when the
// agent resends it, so the analyzer, audit and masking apply as they do for
// any statement. It never approves, lists or claims: claim spends the
// approval, and the agent's resend would then read EXECUTED.
//
// Scope is the sidecar process, not a listener. The control plane scopes a
// status read to the sidecar token, so one server answers for every listener
// and replica of this sidecar, and another sidecar's review reads as not
// found.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Path is where the server answers, so an agent's entry reads
// http://<sidecar>:<port>/mcp.
const Path = "/mcp"

func init() { daemon.RegisterMCPServer(Serve) }

// Serve implements daemon.MCPServe.
func Serve(ctx context.Context, listen string, reviews daemon.ReviewStatusReader, log *slog.Logger) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	log.Info("mcp server listening", "listen", ln.Addr().String(), "path", Path)
	return serve(ctx, ln, NewHandler(ctx, reviews), log)
}

// serve runs handler on ln until ctx ends, and returns only after every
// request has stopped.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: review_wait holds a response open for up to
		// maxWaitTimeout by design. The handler's own ctx ends those calls
		// at shutdown; see NewHandler.
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		return fmt.Errorf("mcp: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("mcp server shutdown timed out; closing open requests", "error", err)
		_ = srv.Close()
	}
	<-served
	return nil
}

// NewHandler returns the MCP endpoint at Path.
//
// Stateless, so no Mcp-Session-Id ties a client to one replica: an agent's
// single entry can sit behind a load balancer. Progress notifications still
// reach the client, because they ride the response of the call they report.
//
// Every tool call ends when ctx does. Shutdown does not cancel requests, and
// the SDK detaches a call's context from its HTTP request for clients older
// than the 2026-07-28 protocol, so without this a review_wait in flight would
// keep polling the plane for minutes after the process was told to stop.
func NewHandler(ctx context.Context, reviews daemon.ReviewStatusReader) http.Handler {
	return newHandler(&tools{reviews: reviews, poll: pollInterval, life: ctx})
}

func newHandler(t *tools) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: "hoop-sidecar", Version: daemon.Version}, nil)
	t.register(server)
	mcpHandler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		// A client that drops the call stops the polling too, on the
		// protocols where the SDK can tell.
		&sdk.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true},
	)
	mux := http.NewServeMux()
	// There is no authentication, so refuse cross-origin browser requests:
	// a web page must not be able to drive the endpoint from a victim's
	// browser. Agents send no Origin and pass.
	mux.Handle(Path, http.NewCrossOriginProtection().Handler(mcpHandler))
	return mux
}
