package proto

import "time"

const (
	PacketKeepAliveType          PacketType = "KeepAlive"
	PacketCloseTCPConnectionType PacketType = "CloseTCPConnection"

	// client starting new connection
	PacketClientGatewayExecType    PacketType = "Client::Gateway::Exec"
	PacketClientGatewayConnectType PacketType = "Client::Gateway::Connect"

	// agent receiving new client connection
	PacketClientAgentConnectType PacketType = "Client::Agent::Connect"

	// agent response to client connection
	PacketClientAgentConnectOKType  PacketType = "Client::Agent::ConnectOK"
	PacketClientAgentConnectErrType PacketType = "Client::Agent::ConnectErr"

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

	DefaultKeepAlive time.Duration = 10 * time.Second

	DevProfile = "dev"

	ProviderOkta = "okta"

	ConnectionOriginAgent  = "agent"
	ConnectionOriginClient = "client"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"
)
