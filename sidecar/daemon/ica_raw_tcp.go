package daemon

// ICARawTCPConfig forwards admitted TCP streams without ICA decoding, OCR,
// redaction or recording. Enable has no deadline; max_sessions still applies.
type ICARawTCPConfig struct {
	Enable bool `json:"enable,omitempty"`
}

func rawTCPEnabled(c *ICARawTCPConfig) bool { return c != nil && c.Enable }

// Raw mode must identify itself in readiness even when the operator only sets
// ica.raw_tcp.enable. Ordinary deployments retain their existing probe format.
func icaReadinessJSONFor(c *Config) bool {
	return c.Admin.ICAReadinessJSON || (c.ICA != nil && rawTCPEnabled(c.ICA.RawTCP))
}

// ICARawTCPStatus mirrors the engine's metadata-only readiness contract.
// CaptureEnabled is always false. Total is a counter, not a lifetime quota.
type ICARawTCPStatus struct {
	Mode           string `json:"mode"`
	Enable         bool   `json:"enable"`
	Ready          bool   `json:"ready"`
	CaptureEnabled bool   `json:"capture_enabled"`
	Active         int    `json:"active"`
	Total          int    `json:"total"`
}

func (s ICARawTCPStatus) rawReady() bool {
	return s.Mode == "raw_tcp" && s.Enable && s.Ready && !s.CaptureEnabled && s.Active >= 0 && s.Total >= s.Active
}
func (s ICARawTCPStatus) inspectedReady() bool {
	return s.Mode == "inspection" && !s.Enable && s.Ready && !s.CaptureEnabled && s.Active == 0 && s.Total >= 0
}
