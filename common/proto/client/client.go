// client must have all package types implemented by a client
package client

const (
	SessionOpenOK              = "ClientSessionOpenOK"
	SessionOpenTimeout         = "ClientSessionOpenTimeout"
	SessionOpenWaitingApproval = "ClientSessionOpenWaitingApproval"
	SessionOpenApproveOK       = "ClientSessionOpenApproveOK"
	SessionOpenAgentOffline    = "ClientSessionOpenAgentOffline"
	SessionClose               = "ClientSessionClose"

	ProxyManagerConnectOK = "ClientProxyManagerConnectOK"

	HttpProxyConnectionWrite = "ClientHttpProxyConnectionWrite"
	TCPConnectionClose       = "ClientTCPConnectionClose"
	TCPConnectionWrite       = "ClientTCPConnectionWrite"
	PGConnectionWrite        = "ClientPGConnectionWrite"
	MySQLConnectionWrite     = "ClientMySQLConnectionWrite"
	MSSQLConnectionWrite     = "ClientMSSQLConnectionWrite"
	MongoDBConnectionWrite   = "ClientMongoDBConnectionWrite"
	WriteStdout              = "ClientWriteStdout"
	WriteStderr              = "ClientWriteStderr"
)
