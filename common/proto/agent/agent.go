// agent must have all types implement by an agent
package agent

const (
	GatewayConnectOK = "AgentGatewayConnectOK"
	SessionOpen      = "AgentSessionOpen"
	SessionClose     = "AgentSessionClose"

	ExecWriteStdin = "AgentExecWriteStdin"

	TerminalWriteStdin = "AgentTerminalWriteStdin"
	TerminalResizeTTY  = "AgentTerminalResizeTTY"
	TerminalClose      = "AgentTerminalClose"

	HttpProxyConnectionWrite = "AgentHttpProxyConnectionWrite"
	TCPConnectionClose       = "AgentCloseTCPConnection"
	TCPConnectionWrite       = "AgentTCPConnectionWrite"
	PGConnectionWrite        = "AgentPGConnectionWrite"
	MySQLConnectionWrite     = "AgentMySQLConnectionWrite"
	MSSQLConnectionWrite     = "AgentMSSQLConnectionWrite"
	MongoDBConnectionWrite   = "AgentMongoDBConnectionWrite"
)
