package daemon

import (
	"path/filepath"
	"time"
)

// ICADiagnosticRecordingConfig authorizes raw ICA TCP capture before decoding.
// Expiry is absolute; there is no relative duration that a restart can extend.
// This block is separate from metadata-only failure_diagnostic_dir.
type ICADiagnosticRecordingConfig struct {
	Until                 string `json:"until,omitempty"`
	OutputDir             string `json:"output_dir,omitempty"`
	AcknowledgeUnredacted bool   `json:"acknowledge_unredacted,omitempty"`
	MaxBytes              int64  `json:"max_bytes,omitempty"`
	MaxSessions           int    `json:"max_sessions,omitempty"`
	SessionTimeoutSec     int    `json:"session_timeout_sec,omitempty"`
}

// Mirror only the engine's pure bounds here: the sidecar root module cannot
// import libhoop-ica. The nested adapter checks the engine validator too. Neither
// path creates capture files during config validation or Build.
func (c *ICADiagnosticRecordingConfig) validate() []string {
	if c == nil {
		return nil
	}
	var p []string
	if !c.AcknowledgeUnredacted {
		p = append(p, "ica.diagnostic_recording requires acknowledge_unredacted: true; no decoding, OCR or masking occurs")
	}
	until, err := time.Parse(time.RFC3339Nano, c.Until)
	if err != nil || until.IsZero() || time.Until(until) > time.Hour {
		p = append(p, "ica.diagnostic_recording.until must be an absolute RFC3339 expiry within one hour")
	}
	if !filepath.IsAbs(c.OutputDir) || filepath.Clean(c.OutputDir) != c.OutputDir {
		p = append(p, "ica.diagnostic_recording.output_dir must be a clean absolute path to a NEW private capture directory")
	}
	if c.MaxBytes < 0 || c.MaxBytes > 1<<30 || c.MaxSessions < 0 || c.MaxSessions > 3 || c.SessionTimeoutSec < 0 || c.SessionTimeoutSec > 900 {
		p = append(p, "ica.diagnostic_recording limits: max_bytes 0..1 GiB, max_sessions 0..3 total admissions, session_timeout_sec 0..900; zero uses engine defaults")
	}
	return p
}

// ICADiagnosticRecordingStatus is content-free engine state. The JSON contract
// matches libhoop-ica's relay; do not infer capture from configured backend names.
type ICADiagnosticRecordingStatus struct {
	Mode           string    `json:"mode"`
	Until          time.Time `json:"recording_until"`
	Ready          bool      `json:"ready"`
	CaptureEnabled bool      `json:"capture_enabled"`
	Active         int       `json:"active"`
	Total          int       `json:"total"`
	Bytes          int64     `json:"recorded_bytes"`
	Failed         bool      `json:"failed"`
}

func (s ICADiagnosticRecordingStatus) equal(other ICADiagnosticRecordingStatus) bool {
	a, b := s, other
	a.Until = time.Time{}
	b.Until = time.Time{}
	return a == b && s.Until.Equal(other.Until)
}
func (s ICADiagnosticRecordingStatus) rawReady() bool {
	return s.Mode == "diagnostic_recording" && s.Ready && s.CaptureEnabled && !s.Failed && s.Active == 0 && s.Total >= 0 && s.Bytes >= 0 && !s.Until.IsZero() && time.Now().Before(s.Until)
}
