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
	PacketExecClientWriteStdinType  PacketType = "Exec::WriteClientStdin"
	PacketExecClientWriteStdoutType PacketType = "Exec::WriteClientStdout"
	PacketExecWriteAgentStdinType   PacketType = "Exec::WriteAgentStdin"
	PacketExecWriteAgentStdoutType  PacketType = "Exec::WriteAgentStdout"
	PacketExecCloseTermType         PacketType = "Exec::CloseTerm"

	PacketPGConnectType     PacketType = "PG::Connect"
	PacketPGWriteServerType PacketType = "PG::WriteServer"
	PacketPGWriteClientType PacketType = "PG::WriteClient"

	SpecGatewayConnectionID      string = "gateway.connection_id"
	SpecClientConnectionID       string = "client.connection_id"
	SpecClientExecExitCodeKey    string = "exec.exit_code"
	SpecClientExecArgsKey        string = "exec.args"
	SpecAgentConnectionParamsKey string = "agent.connection_params"
	SpecAgentEnvVarsKey          string = "agent.env_vars"

	ProtocolPostgresType ProtocolType = "postgres"
	ProtocoTerminalType  ProtocolType = "terminal"

	DefaultKeepAlive time.Duration = 10 * time.Second
)
