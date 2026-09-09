package services

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// The ticket does not make these configurable: a sidecar logs where a
// container platform collects, and serves admin on the port the compose
// stacks and ADR-0005 already use.
const (
	sidecarAuditFile   = "-"
	sidecarAdminListen = "0.0.0.0:19000"
	sidecarLogLevel    = "info"
)

// GenerateSidecarKey returns the token a sidecar authenticates with. The
// "hsc_" prefix keeps it out of the "hpk_" branch of the API auth middleware.
func GenerateSidecarKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return "hsc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// BuildSidecarConfig returns the configuration a sidecar serves.
//
// Connections no longer carry a sidecar assignment, so the gateway has
// nothing to derive listeners from and emits none. A sidecar refuses a
// listener-less config from a control plane, so the gap stops the sidecar
// at startup instead of leaving it relaying traffic nobody inspected.
func BuildSidecarConfig() *daemon.Config {
	return &daemon.Config{
		Audit:    daemon.AuditConfig{File: sidecarAuditFile},
		Admin:    daemon.AdminConfig{Listen: sidecarAdminListen},
		LogLevel: sidecarLogLevel,
	}
}
