package daemon

import (
	"encoding/json"
	"net/http"
)

// ICAInspectionState is a live engine snapshot for an external ICA relay.
// Uninspected readiness requires explicit, coherent engine mode metadata.
type ICAInspectionState struct {
	Ready                     bool
	InspectionEnabled         bool
	GraphicsBypassed          bool
	ProtocolValidationEnabled bool
	DiagnosticRecording       *ICADiagnosticRecordingStatus
	RawTCP                    *ICARawTCPStatus
}

type ICAInspectionReporter interface{ ICAInspectionState() ICAInspectionState }

func writeICAReadiness(w http.ResponseWriter, endpoints []endpointServer, proxyCount int) {
	status := struct {
		Status                    string                        `json:"status"`
		InspectionEnabled         bool                          `json:"inspection_enabled"`
		GraphicsBypassed          bool                          `json:"graphics_bypassed"`
		ProtocolValidationEnabled bool                          `json:"protocol_validation_enabled"`
		DiagnosticRecording       *ICADiagnosticRecordingStatus `json:"diagnostic_recording,omitempty"`
		RawTCP                    *ICARawTCPStatus              `json:"raw_tcp,omitempty"`
	}{Status: "not_ready"}
	ready := len(endpoints) > 0 && proxyCount == 0
	inspection, protocol, bypass := ready, ready, false
	allInspected, allRecording, allRaw := ready, ready, ready
	diagnosticCount, rawCount := 0, 0
	for _, endpoint := range endpoints {
		reporter, ok := endpoint.(ICAInspectionReporter)
		if !ok {
			ready, inspection, protocol, allInspected, allRecording = false, false, false, false, false
			allRaw = false
			continue
		}
		state := reporter.ICAInspectionState()
		ready = ready && state.Ready
		inspection = inspection && state.InspectionEnabled
		protocol = protocol && state.ProtocolValidationEnabled
		bypass = bypass || state.GraphicsBypassed
		inspected := state.InspectionEnabled && state.ProtocolValidationEnabled && !state.GraphicsBypassed
		recording := !state.InspectionEnabled && !state.ProtocolValidationEnabled && state.GraphicsBypassed
		raw := recording
		if d := state.RawTCP; d != nil {
			rawCount++
			if status.RawTCP == nil {
				// All lanes share this forwarder. Retain one sampled counter
				// snapshot; adding lanes must not multiply its counts.
				copy := *d
				status.RawTCP = &copy
			} else if !status.RawTCP.equalMode(*d) {
				ready = false
			}
			inspected = inspected && d.inspectedReady()
			raw = raw && d.rawReady() && state.DiagnosticRecording == nil
			recording = false
		} else {
			raw = false
		}
		if d := state.DiagnosticRecording; d != nil {
			diagnosticCount++
			if status.DiagnosticRecording == nil {
				copy := *d
				status.DiagnosticRecording = &copy
			} else if !status.DiagnosticRecording.equal(*d) {
				ready = false
			}
			inspected = inspected && d.Mode == "inspection" && !d.CaptureEnabled && d.Ready && d.Active == 0
			recording = recording && d.rawReady()
		} else {
			recording = false
		}
		allRaw = allRaw && raw
		allInspected = allInspected && inspected
		allRecording = allRecording && recording
	}
	if (diagnosticCount != 0 && diagnosticCount != len(endpoints)) || (rawCount != 0 && rawCount != len(endpoints)) || (rawCount != 0 && diagnosticCount != 0) {
		ready = false
	}
	status.InspectionEnabled = inspection
	status.ProtocolValidationEnabled = protocol
	status.GraphicsBypassed = bypass
	w.Header().Set("Content-Type", "application/json")
	if ready && (allInspected || allRecording || allRaw) {
		status.Status = "ready"
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}
