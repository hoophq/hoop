// client must have all package types implemented by a client
package pbclient

const (
	SessionOpenOK              = "ClientSessionOpenOK"
	SessionOpenTimeout         = "ClientSessionOpenTimeout"
	SessionOpenWaitingApproval = "ClientSessionOpenWaitingApproval"
	SessionOpenApproveOK       = "ClientSessionOpenApproveOK"
	SessionOpenAgentOffline    = "ClientSessionOpenAgentOffline"
	SessionClose               = "ClientSessionClose"

	ProxyManagerConnectOK = "ClientProxyManagerConnectOK"

	TCPConnectionClose       = "ClientTCPConnectionClose"
	TCPConnectionWrite       = "ClientTCPConnectionWrite"
	PGConnectionWrite        = "ClientPGConnectionWrite"
	MySQLConnectionWrite     = "ClientMySQLConnectionWrite"
	MSSQLConnectionWrite     = "ClientMSSQLConnectionWrite"
	MongoDBConnectionWrite   = "ClientMongoDBConnectionWrite"
	SSHConnectionWrite       = "ClientSSHConnectionWrite"
	WriteStdout              = "ClientWriteStdout"
	WriteStderr              = "ClientWriteStderr"
	HttpProxyConnectionWrite = "ClientHttpProxyConnectionWrite"
)
