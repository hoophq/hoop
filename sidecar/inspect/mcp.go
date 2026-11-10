package inspect

// Metadata keys an mcp lane sets on its statements.
const (
	MetadataMCPMethod      = "mcp.method"
	MetadataMCPTool        = "mcp.tool"
	MetadataMCPSession     = "mcp.session"
	MetadataMCPToolsHidden = "mcp.tools_hidden"

	// The call statement of a tool mcp_gateway.sql names counts its SQL
	// statements; each SQL statement names its argument, dialect and index.
	MetadataMCPSQLStatements = "mcp.sql_statements"
	MetadataMCPSQLArgument   = "mcp.sql_argument"
	MetadataMCPSQLDialect    = "mcp.sql_dialect"
	MetadataMCPSQLIndex      = "mcp.sql_index"
)
