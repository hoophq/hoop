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

	TCPConnectionClose   = "ClientTCPConnectionClose"
	TCPConnectionWrite   = "ClientTCPConnectionWrite"
	PGConnectionWrite    = "ClientPGConnectionWrite"
	MySQLConnectionWrite = "ClientMySQLConnectionWrite"
	WriteStdout          = "ClientWriteStdout"
	WriteStderr          = "ClientWriteStderr"
)
