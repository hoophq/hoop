package proto

import "time"

const (
	PacketKeepAliveType       PacketType = "KeepAlive"
	PacketCloseConnectionType PacketType = "CloseConnection"

	PacketGatewayConnectType    PacketType = "Gateway::Connect"
	PacketAgentConnectType      PacketType = "Agent::Connect"
	PacketGatewayConnectOKType  PacketType = "Gateway::ConnectOK"
	PacketGatewayConnectErrType PacketType = "Gateway::ConnectErr"

	PacketExecRunProcType           PacketType = "Exec::RunProc"
	PacketExecClientWriteStdoutType PacketType = "Exec::WriteClientStdout"
	PacketExecWriteAgentStdinType   PacketType = "Exec::WriteAgentStdin"
	PacketExecCloseTermType         PacketType = "Exec::CloseTerm"

	PacketPGWriteServerType PacketType = "PG::WriteServer"
	PacketPGWriteClientType PacketType = "PG::WriteClient"

	SpecGatewaySessionID         string = "gateway.session_id"
	SpecConnectionType           string = "gateway.connection_type"
	SpecClientConnectionID       string = "client.connection_id"
	SpecClientExecExitCodeKey    string = "exec.exit_code"
	SpecClientExecArgsKey        string = "exec.args"
	SpecAgentConnectionParamsKey string = "agent.connection_params"
	SpecAgentEnvVarsKey          string = "agent.env_vars"

	ProtocolPostgresType ProtocolType = "postgres"
	ProtocoTerminalType  ProtocolType = "terminal"

	DefaultKeepAlive time.Duration = 10 * time.Second

	DevProfile = "dev"

	ProviderOkta = "okta"

	ConnectionOriginAgent  = "agent"
	ConnectionOriginClient = "client"
)
