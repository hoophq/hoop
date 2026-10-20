//go:build linux

package cmd

import (
	"slices"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// The Linux CLI links the ICA engine, so a plane may serve it an ica lane
// and `hoop start sidecar` does not refuse one at startup. The header is
// what a plane reads, so it is what this pins.
func TestLinuxCLIAdvertisesTheICALane(t *testing.T) {
	caps := daemon.SidecarCapabilities()
	if !slices.Contains(caps, "protocol:ica") {
		t.Fatalf("capabilities lack protocol:ica, so the engine is not linked: %v", caps)
	}
}
