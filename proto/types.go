package proto

import "time"

const (
	PacketGatewayComponent string = "gateway"
	PacketAgentComponent   string = "agent"
	PacketClientComponent  string = "client"

	PacketKeepAliveType  string = "KeepAlive"
	PacketDataStreamType string = "DataStream"

	DefaultKeepAlive time.Duration = 10 * time.Second
)
