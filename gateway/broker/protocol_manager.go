package broker

// ProtocolManager manages legacy protocol handlers
type ProtocolManager struct {
	handlers map[string]ProtocolHandler
}

var ProtocolManagerInstance = &ProtocolManager{
	handlers: make(map[string]ProtocolHandler),
}

func init() {
	// Register legacy protocol handlers
	// This we can add more protocols in the future
	ProtocolManagerInstance.RegisterHandler(&RDPHandler{})
}

func (pm *ProtocolManager) RegisterHandler(handler ProtocolHandler) {
	pm.handlers[handler.GetProtocolName()] = handler
}

func (pm *ProtocolManager) GetHandler(protocol string) (ProtocolHandler, bool) {
	handler, exists := pm.handlers[protocol]
	return handler, exists
}
