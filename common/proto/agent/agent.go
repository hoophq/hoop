// agent must have all types implement by an agent
package pbagent

const (
	GatewayConnectOK = "AgentGatewayConnectOK"
	SessionOpen      = "AgentSessionOpen"
	SessionClose     = "AgentSessionClose"

	ExecWriteStdin = "AgentExecWriteStdin"

	TerminalWriteStdin = "AgentTerminalWriteStdin"
	TerminalResizeTTY  = "AgentTerminalResizeTTY"
	TerminalClose      = "AgentTerminalClose"

	TCPConnectionClose       = "AgentCloseTCPConnection"
	TCPConnectionWrite       = "AgentTCPConnectionWrite"
	PGConnectionWrite        = "AgentPGConnectionWrite"
	MySQLConnectionWrite     = "AgentMySQLConnectionWrite"
	MSSQLConnectionWrite     = "AgentMSSQLConnectionWrite"
	MongoDBConnectionWrite   = "AgentMongoDBConnectionWrite"
	OracleConnectionWrite    = "AgentOracleConnectionWrite"
	SSHConnectionWrite       = "AgentSSHConnectionWrite"
	SSMConnectionWrite       = "AgentSSMConnectionWrite"
	HttpProxyConnectionWrite = "AgentHttpProxyConnectionWrite"
	// MCPProxyConnectionWrite carries raw HTTP bytes for a protocol-aware MCP
	// connection (ADR-0004). Same framing as HttpProxyConnectionWrite — the
	// distinct type is what routes the payload to the MCP adapter instead of
	// the byte relay, so both paths can coexist on one agent.
	MCPProxyConnectionWrite = "AgentMCPProxyConnectionWrite"
	// MCPStdioReply carries one JSON-RPC envelope produced by an MCP server
	// running on the CONNECTING USER's machine, travelling client -> agent.
	// It is the return leg of MCPStdioRequest: the agent's tunnelled backend
	// parked a waiter for this connection and this packet wakes it. Distinct
	// from MCPProxyConnectionWrite because that type carries raw HTTP bytes
	// from an MCP client, while this carries a backend's answer.
	MCPStdioReply = "AgentMCPStdioReply"
)
