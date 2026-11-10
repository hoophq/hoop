package daemon

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

type attestedICAServer struct {
	fakeICAServer
	state ICAInspectionState
}

func (s *attestedICAServer) ICAInspectionState() ICAInspectionState { return s.state }

func TestICAReadinessAttestation(t *testing.T) {
	valid := ICAInspectionState{Ready: true, InspectionEnabled: true, ProtocolValidationEnabled: true}
	for _, tc := range []struct {
		name  string
		state ICAInspectionState
		code  int
	}{
		{"ready", valid, 200},
		{"loading", ICAInspectionState{}, 503},
		{"not ready", ICAInspectionState{InspectionEnabled: true, ProtocolValidationEnabled: true}, 503},
		{"inspection disabled", ICAInspectionState{Ready: true, ProtocolValidationEnabled: true}, 503},
		{"bypass", ICAInspectionState{Ready: true, InspectionEnabled: true, ProtocolValidationEnabled: true, GraphicsBypassed: true}, 503},
		{"protocol disabled", ICAInspectionState{Ready: true, InspectionEnabled: true}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeICAReadiness(w, []endpointServer{&attestedICAServer{state: tc.state}}, 0)
			if w.Code != tc.code {
				t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.code == 200 && (result["status"] != "ready" || result["inspection_enabled"] != true || result["graphics_bypassed"] != false || result["protocol_validation_enabled"] != true) {
				t.Fatalf("invalid relay attestation: %#v", result)
			}
		})
	}
}

func TestICAReadinessCannotAttestEmptyOrMixedProcesses(t *testing.T) {
	valid := &attestedICAServer{state: ICAInspectionState{Ready: true, InspectionEnabled: true, ProtocolValidationEnabled: true}}
	for _, tc := range []struct {
		endpoints []endpointServer
		proxies   int
	}{
		{nil, 0}, {[]endpointServer{&fakeICAServer{ready: true}}, 0},
		{[]endpointServer{valid}, 1}, {[]endpointServer{valid, &fakeICAServer{ready: true}}, 0},
	} {
		w := httptest.NewRecorder()
		writeICAReadiness(w, tc.endpoints, tc.proxies)
		if w.Code != 503 {
			t.Fatalf("unattested process passed: %d %s", w.Code, w.Body.String())
		}
	}
}
