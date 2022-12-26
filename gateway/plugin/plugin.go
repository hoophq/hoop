package plugin

import "time"

type (
	Config struct {
		SessionId      string
		ConnectionId   string
		ConnectionName string
		ConnectionType string
		Org            string
		UserID         string
		UserName       string
		Verb           string
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

func (c Config) GetString(key string) string {
	val, ok := c.ParamsData[key]
	if !ok {
		return ""
	}
	return val.(string)
}

func (c Config) Int64(key string) int64 {
	val, ok := c.ParamsData[key]
	if !ok {
		return -1
	}
	v, _ := val.(int64)
	return v
}

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
