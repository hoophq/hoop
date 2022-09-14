package proto

import "time"

const (
	PacketGatewayComponent string = "gateway"
	PacketAgentComponent   string = "agent"
	PacketClientComponent  string = "client"

	PacketKeepAliveType  string = "KeepAlive"
	PacketDataStreamType string = "DataStream"

	DefaultKeepAliveSeconds time.Duration = 10 * time.Second
)
