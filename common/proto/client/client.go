// client must have all package types implemented by a client
package client

const (
	KeepAlive = "ClientKeepAlive"

	SessionOpenOK              = "ClientSessionOpenOK"
	SessionOpenTimeout         = "ClientSessionOpenTimeout"
	SessionOpenWaitingApproval = "ClientSessionOpenWaitingApproval"
	SessionOpenApproveOK       = "ClientSessionOpenApproveOK"
	SessionOpenApproveErr      = "ClientSessionOpenApproveErr"
	SessionOpenAgentOffline    = "ClientSessionOpenAgentOffline"
	SessionClose               = "ClientSessionClose"

	TCPConnectionClose = "ClientTCPConnectionClose"
	TCPConnectionWrite = "ClientTCPConnectionWrite"
	PGConnectionWrite  = "ClientPGConnectionWrite"
	WriteStdout        = "ClientWriteStdout"
	WriteStderr        = "ClientWriteStderr"
)
