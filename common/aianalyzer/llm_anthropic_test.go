package aianalyzer

import (
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// The Messages API expects ONE user turn carrying every tool_result of the
// preceding assistant turn. Emitting one message per result would depend on
// same-role merging, which strict-alternation intermediaries may reject.
func TestAnthropicMessagesFoldsConsecutiveToolResults(t *testing.T) {
	msgs := anthropicMessages([]Message{
		{Role: RoleUser, Content: "analyze"},
		{Role: RoleAssistant, Content: "checking", ToolCalls: []ToolCall{
			{ID: "a", Name: "t1", Arguments: `{}`},
			{ID: "b", Name: "t2", Arguments: `{}`},
		}},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "a", Content: "ra"}},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "b", Content: "rb"}},
	})

	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (user, assistant, folded tool results)", len(msgs))
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("msgs[1].Role = %q, want assistant", msgs[1].Role)
	}
	if msgs[2].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msgs[2].Role = %q, want user", msgs[2].Role)
	}
	if len(msgs[2].Content) != 2 {
		t.Errorf("tool results not folded: got %d blocks in the final turn, want 2", len(msgs[2].Content))
	}
}

// A user turn between tool results must open a new turn rather than absorb the
// following result, and a nil ToolResult must be skipped without corrupting state.
func TestAnthropicMessagesInterleavings(t *testing.T) {
	msgs := anthropicMessages([]Message{
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "a", Content: "ra"}},
		{Role: RoleUser, Content: "follow up"},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "b", Content: "rb"}},
	})
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3; a plain user turn must not absorb the next tool result", len(msgs))
	}
	for i, m := range msgs {
		if len(m.Content) != 1 {
			t.Errorf("msgs[%d] has %d blocks, want 1", i, len(m.Content))
		}
	}

	// A nil ToolResult is skipped and must not leave the folding state armed.
	msgs = anthropicMessages([]Message{
		{Role: RoleTool},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "a", Content: "ra"}},
	})
	if len(msgs) != 1 || len(msgs[0].Content) != 1 {
		t.Errorf("nil tool result mishandled: %+v", msgs)
	}
}
