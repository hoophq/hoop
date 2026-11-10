package ica

import "testing"

func TestUnstartedAndClosedServerCannotAttestInspection(t *testing.T) {
	for _, server := range []*server{{}, {closed: true}} {
		if server.BeginICADrain() {
			t.Fatal("uninitialized server accepted drain")
		}
		state := server.ICAInspectionState()
		if state.Ready || state.InspectionEnabled || state.ProtocolValidationEnabled {
			t.Fatalf("uninitialized server attested inspection: %#v", state)
		}
	}
}
