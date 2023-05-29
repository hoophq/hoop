package types

type ClientStatusType string

const (
	// ClientStatusReady indicates the grpc client is ready to
	// subscribe to a new connection
	ClientStatusReady ClientStatusType = "ready"
	// ClientStatusConnected indicates the client has opened a new session
	ClientStatusConnected ClientStatusType = "connected"
	// ClientStatusDisconnected indicates the grpc client has disconnected
	ClientStatusDisconnected ClientStatusType = "disconnected"
)
