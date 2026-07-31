// client must have all package types implemented by a client
package pbclient

const (
	SessionOpenOK              = "ClientSessionOpenOK"
	SessionOpenTimeout         = "ClientSessionOpenTimeout"
	SessionOpenWaitingApproval = "ClientSessionOpenWaitingApproval"
	SessionOpenApproveOK       = "ClientSessionOpenApproveOK"
	SessionOpenAgentOffline    = "ClientSessionOpenAgentOffline"
	SessionClose               = "ClientSessionClose"
	InteractionClose           = "ClientInteractionClose"
	SessionAnalyzerMetrics     = "ClientSessionAnalyzerMetrics"
	AgentLogs                  = "ClientAgentLogs"

	ProxyManagerConnectOK = "ClientProxyManagerConnectOK"

	TCPConnectionClose       = "ClientTCPConnectionClose"
	TCPConnectionWrite       = "ClientTCPConnectionWrite"
	PGConnectionWrite        = "ClientPGConnectionWrite"
	MySQLConnectionWrite     = "ClientMySQLConnectionWrite"
	MSSQLConnectionWrite     = "ClientMSSQLConnectionWrite"
	MongoDBConnectionWrite   = "ClientMongoDBConnectionWrite"
	OracleConnectionWrite    = "ClientOracleConnectionWrite"
	SSHConnectionWrite       = "ClientSSHConnectionWrite"
	SSMConnectionWrite       = "ClientSSMConnectionWrite"
	WriteStdout              = "ClientWriteStdout"
	WriteStderr              = "ClientWriteStderr"
	HttpProxyConnectionWrite = "ClientHttpProxyConnectionWrite"
	// MCPProxyConnectionWrite is the agent->client half of the protocol-aware
	// MCP path (ADR-0004).
	MCPProxyConnectionWrite = "ClientMCPProxyConnectionWrite"
	// MCPStdioRequest asks the connecting user's CLI to deliver one JSON-RPC
	// envelope to the MCP server running on their own machine, spawning that
	// child on first use. This is the only agent -> client packet that asks
	// the client to originate work rather than consume response bytes; the
	// answer comes back as pbagent.MCPStdioReply.
	MCPStdioRequest = "ClientMCPStdioRequest"
	// MCPStdioClose tells the CLI to terminate the child owned by the MCP
	// backend named in the packet spec. Sent when the MCP session ends while
	// the hoop session stays open, so a user who reconnects their MCP client
	// does not accumulate orphaned server processes.
	MCPStdioClose = "ClientMCPStdioClose"
)
