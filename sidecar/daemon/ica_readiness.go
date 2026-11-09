package daemon

import (
	"encoding/json"
	"net/http"
)

// ICAInspectionState is a live engine snapshot for an external ICA relay.
// An endpoint that cannot attest inspection never authorizes relay admission.
type ICAInspectionState struct {
	Ready                     bool
	InspectionEnabled         bool
	GraphicsBypassed          bool
	ProtocolValidationEnabled bool
}

// ICAInspectionReporter is optional, keeping existing engine embedders compatible.
// Only an initialized ICA engine may implement this attestation.
type ICAInspectionReporter interface {
	ICAInspectionState() ICAInspectionState
}

func writeICAReadiness(w http.ResponseWriter, endpoints []endpointServer, proxyCount int) {
	status := struct {
		Status                    string `json:"status"`
		InspectionEnabled         bool   `json:"inspection_enabled"`
		GraphicsBypassed          bool   `json:"graphics_bypassed"`
		ProtocolValidationEnabled bool   `json:"protocol_validation_enabled"`
	}{Status: "not_ready"}
	ready := len(endpoints) > 0 && proxyCount == 0
	inspection, protocol, bypass := ready, ready, false
	for _, endpoint := range endpoints {
		reporter, ok := endpoint.(ICAInspectionReporter)
		if !ok {
			ready, inspection, protocol = false, false, false
			continue
		}
		state := reporter.ICAInspectionState()
		ready = ready && state.Ready
		inspection = inspection && state.InspectionEnabled
		protocol = protocol && state.ProtocolValidationEnabled
		bypass = bypass || state.GraphicsBypassed
	}
	status.InspectionEnabled = inspection
	status.GraphicsBypassed = bypass
	status.ProtocolValidationEnabled = protocol
	w.Header().Set("Content-Type", "application/json")
	if ready && inspection && protocol && !bypass {
		status.Status = "ready"
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}
