package daemon

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func recordingConfigForTest(t *testing.T) *ICADiagnosticRecordingConfig {
	t.Helper()
	return &ICADiagnosticRecordingConfig{Until: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), OutputDir: filepath.Join(t.TempDir(), "NEW-capture"), AcknowledgeUnredacted: true}
}
func recordingDocument(t *testing.T, c *ICADiagnosticRecordingConfig, admin bool, listeners string) []byte {
	t.Helper()
	r, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(`{"admin":{"ica_readiness_json":` + map[bool]string{true: "true", false: "false"}[admin] + `},"ica":{"diagnostic_recording":` + string(r) + `},"listeners":[` + listeners + `]}`)
}
func TestICARecordingConfigValidationAndCapability(t *testing.T) {
	c := recordingConfigForTest(t)
	cfg, err := LoadConfigBytes(recordingDocument(t, c, true, icaSingle))
	if err != nil {
		t.Fatal(err)
	}
	rt := icaRuntimeFor(cfg)
	if rt.DiagnosticRecording == nil || *rt.DiagnosticRecording != *c {
		t.Fatal("recording configuration lost", rt)
	}
	cfg.ICA.DiagnosticRecording.Until = "mutated"
	if rt.DiagnosticRecording.Until == "mutated" {
		t.Fatal("runtime retains mutable config pointer")
	}
	cfg.ICA.DiagnosticRecording.Until = c.Until
	caps := append(SidecarCapabilities(), "protocol:ica")
	without := slices.DeleteFunc(slices.Clone(caps), func(s string) bool { return s == "ica_diagnostic_recording" })
	if err = CheckServable(*cfg, Handshake{Capabilities: without}); err == nil || !strings.Contains(err.Error(), "ica_diagnostic_recording") {
		t.Fatal("old build can receive recording config", err)
	}
	if err = CheckServable(*cfg, Handshake{Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ICADiagnosticRecordingConfig){
		"acknowledgement":   func(c *ICADiagnosticRecordingConfig) { c.AcknowledgeUnredacted = false },
		"relative deadline": func(c *ICADiagnosticRecordingConfig) { c.Until = "10m" },
		"unbounded deadline": func(c *ICADiagnosticRecordingConfig) {
			c.Until = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		},
		"relative output": func(c *ICADiagnosticRecordingConfig) { c.OutputDir = "relative" },
		"byte limit":      func(c *ICADiagnosticRecordingConfig) { c.MaxBytes = 1<<30 + 1 },
		"session limit":   func(c *ICADiagnosticRecordingConfig) { c.MaxSessions = 4 },
		"time limit":      func(c *ICADiagnosticRecordingConfig) { c.SessionTimeoutSec = 901 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := *c
			change(&copy)
			if _, err := LoadConfigBytes(recordingDocument(t, &copy, true, icaSingle)); err == nil {
				t.Fatal("unsafe recording config accepted")
			}
		})
	}
	for _, doc := range [][]byte{recordingDocument(t, c, false, icaSingle), recordingDocument(t, c, true, icaSingle+`,`+`{"name":"other","listen":"127.0.0.1:25000","upstream":"127.0.0.1:5432","protocol":"postgres"}`), recordingDocument(t, c, true, strings.Replace(icaSingle, `"tenant_id":`, `"edt":true,"tenant_id":`, 1)), recordingDocument(t, c, true, "")} {
		if _, err = LoadConfigBytes(doc); err == nil {
			t.Fatal("unattested/mixed/EDT recording process accepted", string(doc))
		}
	}
	expired := *c
	expired.Until = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err = LoadConfigBytes(recordingDocument(t, &expired, true, icaSingle)); err != nil {
		t.Fatal("expired startup cannot restore inspection", err)
	}
}

func rawRecordingState(until time.Time) ICAInspectionState {
	return ICAInspectionState{Ready: true, GraphicsBypassed: true, DiagnosticRecording: &ICADiagnosticRecordingStatus{Mode: "diagnostic_recording", Until: until, Ready: true, CaptureEnabled: true}}
}
func TestICARecordingReadinessStrictAttestationAndRestore(t *testing.T) {
	until := time.Now().Add(time.Minute).UTC()
	for name, change := range map[string]func(*ICAInspectionState){
		"ready":                  func(*ICAInspectionState) {},
		"busy":                   func(s *ICAInspectionState) { s.DiagnosticRecording.Active = 1 },
		"failed capture":         func(s *ICAInspectionState) { s.DiagnosticRecording.Failed = true },
		"capture off":            func(s *ICAInspectionState) { s.DiagnosticRecording.CaptureEnabled = false },
		"expired":                func(s *ICAInspectionState) { s.DiagnosticRecording.Until = time.Now().Add(-time.Second) },
		"inspection conflict":    func(s *ICAInspectionState) { s.InspectionEnabled = true },
		"protocol conflict":      func(s *ICAInspectionState) { s.ProtocolValidationEnabled = true },
		"missing capture status": func(s *ICAInspectionState) { s.DiagnosticRecording = nil },
		"restoring":              func(s *ICAInspectionState) { s.DiagnosticRecording.Mode = "restoring" },
	} {
		t.Run(name, func(t *testing.T) {
			state := rawRecordingState(until)
			change(&state)
			w := httptest.NewRecorder()
			writeICAReadiness(w, []endpointServer{&attestedICAServer{state: state}}, 0)
			want := 503
			if name == "ready" {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if want == 200 {
				var body map[string]any
				json.Unmarshal(w.Body.Bytes(), &body)
				if body["inspection_enabled"] != false || body["protocol_validation_enabled"] != false || body["graphics_bypassed"] != true || body["diagnostic_recording"] == nil {
					t.Fatal(body)
				}
			}
		})
	}
	// Every lane must attest the same process-wide recorder and coherent mode.
	a, b := rawRecordingState(until), rawRecordingState(until)
	b.DiagnosticRecording.Until = until.Add(time.Second)
	w := httptest.NewRecorder()
	writeICAReadiness(w, []endpointServer{&attestedICAServer{state: a}, &attestedICAServer{state: b}}, 0)
	if w.Code != 503 {
		t.Fatal("mismatched windows passed")
	}
	b = ICAInspectionState{Ready: true, InspectionEnabled: true, ProtocolValidationEnabled: true}
	w = httptest.NewRecorder()
	writeICAReadiness(w, []endpointServer{&attestedICAServer{state: a}, &attestedICAServer{state: b}}, 0)
	if w.Code != 503 {
		t.Fatal("mixed raw and inspected lanes passed")
	}
	// Historical capture failure does not prevent a now healthy inspecting runtime.
	b.DiagnosticRecording = &ICADiagnosticRecordingStatus{Mode: "inspection", Until: until.Add(-time.Hour), Ready: true, Failed: true, Total: 1}
	w = httptest.NewRecorder()
	writeICAReadiness(w, []endpointServer{&attestedICAServer{state: b}}, 0)
	if w.Code != 200 {
		t.Fatal("inspection not restored", w.Body.String())
	}
}
