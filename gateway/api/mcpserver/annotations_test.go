package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The Claude and OpenAI directories reject a tool without a title or an
// explicit hint, so the check runs on the tools/list wire output.
func TestEveryToolHasTitleAndExplicitHints(t *testing.T) {
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := newServer(nil).Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tool.Name, err)
		}
		var wire struct {
			Title       string           `json:"title"`
			Annotations map[string]*bool `json:"annotations"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("%s: unmarshal: %v", tool.Name, err)
		}
		if wire.Title == "" {
			t.Errorf("%s: no title", tool.Name)
		}
		for _, hint := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			if wire.Annotations[hint] == nil {
				t.Errorf("%s: no explicit %s", tool.Name, hint)
			}
		}
		if readOnly, destructive := wire.Annotations["readOnlyHint"], wire.Annotations["destructiveHint"]; readOnly != nil && destructive != nil && *readOnly && *destructive {
			t.Errorf("%s: read-only tool marked destructive", tool.Name)
		}
	}
}
