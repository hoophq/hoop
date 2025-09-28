package broker

// ProtocolHandler defines the interface for protocol-specific handlers
type ProtocolHandler interface {
	// HandleSessionStarted processes the initial session start message
	// we can do protocol-specific setup here with handshakes if needed
	HandleSessionStarted(session *Session, msg *WebSocketMessage) error
	HandleData(session *Session, msg *WebSocketMessage) error
	GetProtocolName() string
}
