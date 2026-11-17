package ica

import (
	"errors"
	"fmt"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/libhoop-ica/ica"
)

// diagnosticConfig validates without opening a directory or starting capture.
// Files are created only once, after the normal shared runtime has loaded.
func diagnosticConfig(c *daemon.ICADiagnosticRecordingConfig) (*ica.DiagnosticRecordingConfig, error) {
	if c == nil {
		return nil, nil
	}
	if c.SessionTimeoutSec < 0 || c.SessionTimeoutSec > 900 {
		return nil, errors.New("ica.diagnostic_recording.session_timeout_sec must be 0..900")
	}
	until, err := time.Parse(time.RFC3339Nano, c.Until)
	if err != nil {
		return nil, errors.New("ica.diagnostic_recording.until must be an absolute RFC3339 timestamp")
	}
	out := &ica.DiagnosticRecordingConfig{OutputDir: c.OutputDir, Until: until.UTC(), AcknowledgeUnredacted: c.AcknowledgeUnredacted, MaxBytes: c.MaxBytes, MaxSessions: c.MaxSessions, SessionTimeout: time.Duration(c.SessionTimeoutSec) * time.Second}
	if err := ica.ValidateDiagnosticRecordingConfig(*out); err != nil {
		return nil, fmt.Errorf("ica.diagnostic_recording: %w", err)
	}
	return out, nil
}
