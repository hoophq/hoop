package core

import "time"

type (
	ParamsData map[string]any
	PacketData interface {
		GetSpec() map[string][]byte
		GetType() string
		GetPayload() []byte
	}
	Plugin interface {
		Name() string
		OnStartup(PluginConfig) error
		OnConnect(ParamsData) error
		OnDisconnect(ParamsData) error
		OnReceive(sessionID string, pkt PacketData) error
		OnShutdown()
	}
	PluginConfig interface {
		// Enabled returns true if the plugin is enabled
		Enabled() bool
		// Config should return the known structures for this plugin to work.
		Config() ParamsData
	}
	StorageWriter interface {
		Write(ParamsData) error
	}
)

func (c ParamsData) Get(key string) any {
	return c[key]
}

func (c ParamsData) GetByte(key string) []byte {
	val, ok := c[key]
	if !ok {
		return nil
	}
	return val.([]byte)
}

// GetString returns the underlying value as string, it returns empty
// if the key isn't found
func (c ParamsData) GetString(key string) string {
	val, ok := c[key]
	if !ok {
		return ""
	}
	return val.(string)
}

// GetDuration returns the underlying value as duration, it returns 0
// if the key isn't found
func (c ParamsData) GetDuration(key string) time.Duration {
	val, ok := c[key]
	if !ok {
		return 0
	}
	return val.(time.Duration)
}

func (c ParamsData) GetTime(key string) *time.Time {
	val, ok := c[key]
	if !ok {
		return nil
	}
	return val.(*time.Time)
}
