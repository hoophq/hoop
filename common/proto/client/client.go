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
)
