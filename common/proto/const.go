package proto

import "time"

const (
	PacketKeepAliveType          PacketType = "KeepAlive"
	PacketCloseTCPConnectionType PacketType = "CloseTCPConnection"

	// client->agent connection
	PacketClientGatewayConnectType    PacketType = "Client::Gateway::Connect"
	PacketClientGatewayConnectErrType PacketType = "Client::Gateway::ConnectErr"
	PacketClientAgentConnectType      PacketType = "Client::Agent::Connect"
	PacketClientAgentConnectOKType    PacketType = "Client::Agent::ConnectOK"
	PacketClientAgentConnectErrType   PacketType = "Client::Agent::ConnectErr"

	// client->agent exec
	PacketClientGatewayExecType      PacketType = "Client::Gateway::Exec"
	PacketClientGatewayExecErrType   PacketType = "Client::Gateway::ExecErr"
	PacketClientGatewayExecWaitType    PacketType = "Client::Gateway::ExecWait"
	PacketClientGatewayExecApproveType PacketType = "Client::Gateway::ExecApprove"
	PacketClientGatewayExecRejectType  PacketType = "Client::Gateway::ExecReject"
	PacketClientExecAgentOfflineType PacketType = "Client::Gateway::ExecAgentOffline"
	PacketClientAgentExecType        PacketType = "Client::Agent::Exec"
	PacketClientAgentExecOKType      PacketType = "Client::Agent::ExecOK"
	PacketClientAgentExecErrType     PacketType = "Client::Agent::ExecErr"

	// terminal messages
	PacketTerminalRunProcType           PacketType = "Terminal::RunProc"
	PacketTerminalClientWriteStdoutType PacketType = "Terminal::Client::WriteStdout"
	PacketTerminalWriteAgentStdinType   PacketType = "Terminal::Agent::WriteStdin"
	PacketTerminalCloseType             PacketType = "Terminal::Close"

	// PG protocol messages
	PacketPGWriteServerType PacketType = "PG::WriteServer"
	PacketPGWriteClientType PacketType = "PG::WriteClient"

	SpecGatewaySessionID          string = "gateway.session_id"
	SpecConnectionType            string = "gateway.connection_type"
	SpecDLPTransformationSummary  string = "dlp.transformation_summary"
	SpecClientConnectionID        string = "client.connection_id"
	SpecClientExecExitCodeKey     string = "terminal.exit_code"
	SpecClientExecArgsKey         string = "terminal.args"
	SpecAgentConnectionParamsKey  string = "agent.connection_params"
	SpecAgentGCPRawCredentialsKey string = "agent.gcp_credentials"
	SpecReviewDataKey             string = "review.data"

	SpecClientGatewayExecPayload string = "exec.payload"

	DefaultKeepAlive time.Duration = 10 * time.Second

	DevProfile = "dev"

	ProviderOkta = "okta"

	ConnectionOriginAgent  = "agent"
	ConnectionOriginClient = "client"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"

	ClientVerbConnect = "connect"
	ClientVerbExec = "exec"
)
