package mcpgateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/mcpproxy/jsonrpc"
)

// The tool result the agent reads carries the detail. The Reason mcpproxy
// emits to its audit sinks does not.
func TestADeniedToolResultSendsTheDetailToTheClientOnly(t *testing.T) {
	req := &jsonrpc.Message{ID: jsonrpc.ID{Raw: json.RawMessage("3")}}
	d := deniedToolResult(req, daemon.MCPVerdict{Message: "unbounded delete", Detail: "no filter on the call", Rule: "tools"})
	if d.Reply == nil {
		t.Fatal("no reply")
	}
	if want := "denied by the hoop sidecar (rule tools): unbounded delete: no filter on the call"; !strings.Contains(string(d.Reply.Result), want) {
		t.Errorf("reply = %s, want %q", d.Reply.Result, want)
	}
	if d.Reason != "denied by the hoop sidecar (rule tools): unbounded delete" {
		t.Errorf("reason = %q", d.Reason)
	}
}
