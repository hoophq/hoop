package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ReviewWaitTool names the MCP tool that waits on a review. A return-mode
// denial tells the agent to call it, so sidecar/mcp registers it under this
// name and the two cannot drift.
const ReviewWaitTool = "review_wait"

// MCPConfig is the top-level "mcp" block: an MCP server that answers agents
// about the reviews this sidecar filed (ADR-0021).
//
// It has no authentication, the same as the listener ports: it returns a
// review's status, listener and rule for a known id, and never statement
// text. Operators bind it where only agents reach it.
type MCPConfig struct {
	// Listen is the TCP address the server binds, for example
	// "127.0.0.1:8765". Required when the block is present.
	Listen string `json:"listen,omitempty"`
}

func (m *MCPConfig) validate() []string {
	if m == nil {
		return nil
	}
	if m.Listen == "" {
		return []string{`mcp: "listen" is required; remove the block to turn the MCP server off`}
	}
	_, port, err := net.SplitHostPort(m.Listen)
	if err != nil {
		return []string{fmt.Sprintf("mcp: listen %q: %v", m.Listen, err)}
	}
	// SplitHostPort accepts any port text; a bad one would pass here and
	// only fail at bind, after the plane had already stored the document.
	if _, err := net.LookupPort("tcp", port); err != nil {
		return []string{fmt.Sprintf("mcp: listen %q: %v", m.Listen, err)}
	}
	return nil
}

// ReviewStatus is one review as GET /api/sidecars/reviews/:id reports it.
//
// It has no forward flag: a status read never releases a statement. Status
// is the plane's own word and is not narrowed here; the MCP server decides
// what an agent does next with it.
type ReviewStatus struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	ListenerName    string     `json:"listener_name"`
	ApprovalRule    string     `json:"approval_rule"`
	CreatedAt       time.Time  `json:"created_at"`
	DecidedAt       *time.Time `json:"decided_at"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
}

var (
	// ErrReviewNotFound is the plane answering that this sidecar has no
	// such review. Another sidecar's review reads the same, because the
	// token scopes the lookup.
	ErrReviewNotFound = errors.New("review not found")

	// ErrPlaneTooOld is a plane with no status route. It must not read as
	// ErrReviewNotFound: the review may exist, and "not found" would tell an
	// agent to give up on a statement a human can still approve.
	ErrPlaneTooOld = errors.New("the control plane is older than this sidecar and " +
		"cannot report review status; upgrade the control plane")
)

// ReviewStatusReader reads the reviews of this sidecar. It never claims one.
type ReviewStatusReader interface {
	ReviewStatus(ctx context.Context, id string) (ReviewStatus, error)

	// ListReviews returns this sidecar's reviews, newest first. An empty
	// status lists every status; limit is 1 to MaxReviewListLimit.
	ListReviews(ctx context.Context, status string, limit int) ([]ReviewStatus, error)
}

// MaxReviewListLimit is the most reviews one ListReviews returns, the plane's
// own bound on GET /api/sidecars/reviews.
const MaxReviewListLimit = 200

// reviewStatusReader is the plane as a ReviewStatusReader, or nil when this
// process has none. A nil *controlPlane must not become a non-nil interface.
func (c *Config) reviewStatusReader() ReviewStatusReader {
	if c.cp == nil {
		return nil
	}
	return c.cp
}

// MCPServe runs the MCP server on listen until ctx ends. It returns nil after
// ctx ends and an error when it cannot serve, such as a bind failure.
type MCPServe func(ctx context.Context, listen string, reviews ReviewStatusReader, log *slog.Logger) error

var (
	mcpMu    sync.Mutex
	mcpServe MCPServe
)

// RegisterMCPServer links an MCP server into this binary. sidecar/mcp calls it
// from init, so a binary gets one by importing that module. It panics on a
// second registration, because import order must not pick a winner.
func RegisterMCPServer(serve MCPServe) {
	if serve == nil {
		panic("sidecar/daemon: RegisterMCPServer called with a nil server")
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	if mcpServe != nil {
		panic("sidecar/daemon: duplicate MCP server registration")
	}
	mcpServe = serve
}

func registeredMCPServer() MCPServe {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	return mcpServe
}

// checkMCP refuses an "mcp" block this process cannot serve. It runs in Run
// and Validate, not in Config.Validate: the gateway validates sidecar
// documents with the same decoder and links no MCP server.
//
// A plane counts when this process holds a connection (cfg.cp) or, for a
// -validate run, when the file or the environment names one. Same rule as
// holdsWithoutAPlane.
func checkMCP(cfg *Config) error {
	if cfg.MCP == nil {
		return nil
	}
	if registeredMCPServer() == nil {
		return errors.New(`config has an "mcp" block but this build has no MCP server; ` +
			"build github.com/hoophq/hoop/sidecar/cmd, or remove the block")
	}
	if cfg.cp == nil && !cfg.controlPlaneConfigured() {
		return fmt.Errorf(`config has an "mcp" block but no control plane; review status `+
			"comes from the control plane, so set %s or \"control_plane_url\", or remove the block",
			ControlPlaneURLEnv)
	}
	return nil
}
