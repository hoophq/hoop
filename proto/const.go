package proto

import "time"

const (
	PacketKeepAliveType       PacketType = "KeepAlive"
	PacketDataStreamType      PacketType = "DataStream"
	PacketCloseConnectionType PacketType = "CloseConnection"

	PacketGatewayConnectType    PacketType = "Gateway::Connect"
	PacketAgentConnectType      PacketType = "Agent::Connect"
	PacketGatewayConnectOKType  PacketType = "Gateway::ConnectOK"
	PacketGatewayConnectErrType PacketType = "Gateway::ConnectErr"

	PacketPGConnectType     PacketType = "PG::Connect"
	PacketPGWriteServerType PacketType = "PG::WriteServer"
	PacketPGWriteClientType PacketType = "PG::WriteClient"

	SpecGatewayConnectionID string = "gateway.connection_id"
	SpecClientConnectionID  string = "client.connection_id"
	SpecAgentEnvVars        string = "agent.env_vars"

	ProtocolPostgresType ProtocolType = "postgres"

	DefaultKeepAlive time.Duration = 10 * time.Second
)
