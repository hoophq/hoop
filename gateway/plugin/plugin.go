package plugin

import "time"

type (
	Config struct {
		SessionId      string
		ConnectionId   string
		ConnectionName string
		ConnectionType string
		Org            string
		User           string
		Hostname       string
		MachineId      string
		KernelVersion  string
		ParamsData     map[string]any
	}
)

func (c Config) Get(key string) any {
	return c.ParamsData[key]
}

func (c Config) GetByte(key string) []byte {
	val, ok := c.ParamsData[key]
	if !ok {
		return nil
	}
	return val.([]byte)
}

// GetString returns the underlying value as string, it returns empty
// if the key isn't found
func (c Config) GetString(key string) string {
	val, ok := c.ParamsData[key]
	if !ok {
		return ""
	}
	return val.(string)
}

// GetDuration returns the underlying value as duration, it returns 0
// if the key isn't found
func (c Config) GetDuration(key string) time.Duration {
	val, ok := c.ParamsData[key]
	if !ok {
		return 0
	}
	return val.(time.Duration)
}

func (c Config) GetTime(key string) *time.Time {
	val, ok := c.ParamsData[key]
	if !ok {
		return nil
	}
	return val.(*time.Time)
}
