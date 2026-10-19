package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Each tool sets all three hints: the Claude and OpenAI directories reject a
// tool with a missing or inaccurate hint, and a nil DestructiveHint or
// OpenWorldHint means true to the client.

// readOnlyHints marks a tool that only reads state.
func readOnlyHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
}

// additiveHints marks a tool that only inserts new records. A duplicate fails
// or adds a new row; it never overwrites one.
func additiveHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
}

// destructiveHints marks a tool that can overwrite, delete or revoke gateway
// state.
func destructiveHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}
}

// openWorldHints marks a tool that runs, or releases, a command on a
// connection. The command can change anything the connection reaches, and a
// shell, custom or HTTP connection reaches the public internet.
func openWorldHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)}
}
