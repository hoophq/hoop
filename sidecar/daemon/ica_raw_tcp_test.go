package daemon

import (
	"encoding/json"
	"github.com/hoophq/hoop/sidecar/audit"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func rawTCPDocument(enabled bool, listeners string) []byte {
	enable := "false"
	if enabled {
		enable = "true"
	}
	return []byte(`{"ica":{"max_sessions":2,"raw_tcp":{"enable":` + enable + `}},"listeners":[` + listeners + `]}`)
}

func TestICARawTCPConfigAndCapability(t *testing.T) {
	cfg, err := LoadConfigBytes(rawTCPDocument(true, icaSingle))
	if err != nil {
		t.Fatal(err)
	}
	if !icaReadinessJSONFor(cfg) {
		t.Fatal("single toggle did not enable mode-aware readiness")
	}
	rt := icaRuntimeFor(cfg)
	cfg.ICA.RawTCP.Enable = false
	if !rt.RawTCP.Enable {
		t.Fatal("runtime retained mutable config")
	}
	cfg.ICA.RawTCP.Enable = true
	caps := append(SidecarCapabilities(), "protocol:ica")
	without := slices.DeleteFunc(slices.Clone(caps), func(s string) bool { return s == "ica_raw_tcp" })
	if err = CheckServable(*cfg, Handshake{Capabilities: without}); err == nil || !strings.Contains(err.Error(), "ica_raw_tcp") {
		t.Fatal("old sidecar accepted raw TCP config", err)
	}
	if err = CheckServable(*cfg, Handshake{Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	// The toggle is restart-bound, never a hot rule reload.
	before, err := BaselineDoc(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ICA.RawTCP.Enable = false
	after, err := BaselineDoc(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("raw TCP omitted from restart baseline")
	}
	if icaReadinessJSONFor(cfg) {
		t.Fatal("disabled block changed default readiness format")
	}
	for name, listeners := range map[string]string{
		"missing listener": "",
		"mixed protocols":  icaSingle + `,{"name":"other","listen":"127.0.0.1:25000","upstream":"127.0.0.1:5432","protocol":"postgres"}`,
		"EDT":              strings.Replace(icaSingle, `"tenant_id":`, `"edt":true,"tenant_id":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfigBytes(rawTCPDocument(true, listeners)); err == nil {
				t.Fatal("invalid raw process accepted")
			}
		})
	}
	recording := recordingConfigForTest(t)
	doc := string(recordingDocument(t, recording, true, icaSingle))
	doc = strings.Replace(doc, `"ica":{`, `"ica":{"raw_tcp":{"enable":true},`, 1)
	if _, err = LoadConfigBytes([]byte(doc)); err == nil {
		t.Fatal("recording and raw TCP both enabled")
	}
	doc = strings.Replace(doc, `"raw_tcp":{"enable":true}`, `"raw_tcp":{"enable":false}`, 1)
	if _, err = LoadConfigBytes([]byte(doc)); err != nil {
		t.Fatal("disabled raw TCP interfered with recording", err)
	}
}

func rawTCPState() ICAInspectionState {
	return ICAInspectionState{Ready: true, GraphicsBypassed: true, RawTCP: &ICARawTCPStatus{Mode: "raw_tcp", Enable: true, Ready: true, Active: 2, Total: 5}}
}
func TestICARawTCPReadinessAndRestore(t *testing.T) {
	for name, change := range map[string]func(*ICAInspectionState){
		"ready with concurrent sessions": func(*ICAInspectionState) {},
		"capacity full":                  func(s *ICAInspectionState) { s.Ready = false },
		"disabled":                       func(s *ICAInspectionState) { s.RawTCP.Enable = false },
		"capture enabled":                func(s *ICAInspectionState) { s.RawTCP.CaptureEnabled = true },
		"restoring":                      func(s *ICAInspectionState) { s.RawTCP.Mode = "restoring" },
		"missing status":                 func(s *ICAInspectionState) { s.RawTCP = nil },
		"invalid counts":                 func(s *ICAInspectionState) { s.RawTCP.Total = 1 },
		"inspection conflict":            func(s *ICAInspectionState) { s.InspectionEnabled = true },
		"protocol conflict":              func(s *ICAInspectionState) { s.ProtocolValidationEnabled = true },
		"dual mode":                      func(s *ICAInspectionState) { s.DiagnosticRecording = rawRecordingStateForConflict() },
	} {
		t.Run(name, func(t *testing.T) {
			s := rawTCPState()
			change(&s)
			w := httptest.NewRecorder()
			writeICAReadiness(w, []endpointServer{&attestedICAServer{state: s}}, 0)
			want := 503
			if name == "ready with concurrent sessions" {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	a := rawTCPState()
	b := ICAInspectionState{Ready: true, InspectionEnabled: true, ProtocolValidationEnabled: true}
	w := httptest.NewRecorder()
	writeICAReadiness(w, []endpointServer{&attestedICAServer{state: a}, &attestedICAServer{state: b}}, 0)
	if w.Code != 503 {
		t.Fatal("mixed raw/normal lanes accepted")
	}
	b.RawTCP = &ICARawTCPStatus{Mode: "inspection", Ready: true, Total: 5}
	w = httptest.NewRecorder()
	writeICAReadiness(w, []endpointServer{&attestedICAServer{state: b}}, 0)
	if w.Code != 200 {
		t.Fatal("drained controller did not restore readiness", w.Body.String())
	}
	// Exercise the health handler using the same effective setting as Run.
	cfg, err := LoadConfigBytes(rawTCPDocument(true, icaSingle))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handleHealth(mux, healthState{lanes: testLaneSet([]endpointServer{&attestedICAServer{state: a}}, 0), icaReadinessJSON: icaReadinessJSONFor(cfg)})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	var body map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || body["raw_tcp"] == nil || body["inspection_enabled"] != false {
		t.Fatal(w.Code, w.Body.String(), err)
	}
}
func rawRecordingStateForConflict() *ICADiagnosticRecordingStatus {
	return &ICADiagnosticRecordingStatus{Mode: "diagnostic_recording", Ready: true, CaptureEnabled: true}
}

func TestICARawTCPAuditExplicitModeWithoutContent(t *testing.T) {
	sink := audit.NewMemorySink(4)
	b := newICAAuditBridge(sink, "vdi", "tenant", true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	data := map[string]any{"mode": "raw_tcp", "inspection_enabled": false, "protocol_validation_enabled": false, "graphics_bypassed": true, "capture_enabled": false, "redact_video": false, "payload": "never retain this"}
	for _, name := range []string{icaEventSessionStart, icaEventSessionEnd} {
		if name == icaEventSessionEnd {
			data["decision"] = "release_uninspected"
		}
		if err := b.observe(ICAEvent{Name: name, SessionID: "raw-test", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	rows := sink.Events()
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if !row.Allowed || row.Metadata["mode"] != "raw_tcp" || row.Metadata["inspection_enabled"] != "false" || row.Metadata["protocol_validation_enabled"] != "false" || row.Metadata["capture_enabled"] != "false" || row.Metadata["graphics_bypassed"] != "true" || row.Statement != "" || row.Metadata["payload"] != "" {
			t.Fatal("untruthful raw audit", row)
		}
	}
}

// Lane reads sample the same process-wide forwarder at different instants.
// Opening/closing connections between those reads must not withdraw a healthy
// pod, and reporting must not sum copies of that shared counter.
func TestICARawTCPReadinessSerialSnapshots(t *testing.T) {
	for name, counts := range map[string][2]int{
		"opened":            {3, 6},
		"closed":            {1, 5},
		"opened and closed": {2, 6},
		"all closed":        {0, 9},
	} {
		t.Run(name, func(t *testing.T) {
			a, b := rawTCPState(), rawTCPState()
			b.RawTCP.Active, b.RawTCP.Total = counts[0], counts[1]
			for _, states := range [][2]ICAInspectionState{{a, b}, {b, a}} {
				w := httptest.NewRecorder()
				writeICAReadiness(w, []endpointServer{&attestedICAServer{state: states[0]}, &attestedICAServer{state: states[1]}}, 0)
				if w.Code != 200 {
					t.Fatalf("healthy sampled counters made pod unready: %d %s", w.Code, w.Body.String())
				}
				var body struct {
					RawTCP *ICARawTCPStatus `json:"raw_tcp"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.RawTCP == nil || *body.RawTCP != *states[0].RawTCP {
					t.Fatalf("response must keep one coherent shared snapshot, got %+v", body.RawTCP)
				}
			}
		})
	}
}

func TestICARawTCPReadinessRejectsUnhealthyLaterSnapshot(t *testing.T) {
	for name, change := range map[string]func(*ICAInspectionState){
		"mode":                 func(s *ICAInspectionState) { s.RawTCP.Mode = "restoring" },
		"enable":               func(s *ICAInspectionState) { s.RawTCP.Enable = false },
		"controller readiness": func(s *ICAInspectionState) { s.RawTCP.Ready = false },
		"capture":              func(s *ICAInspectionState) { s.RawTCP.CaptureEnabled = true },
		"negative active":      func(s *ICAInspectionState) { s.RawTCP.Active = -1 },
		"negative total":       func(s *ICAInspectionState) { s.RawTCP.Total = -1 },
		"total below active":   func(s *ICAInspectionState) { s.RawTCP.Total = 1 },
		"lane capacity":        func(s *ICAInspectionState) { s.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			a, b := rawTCPState(), rawTCPState()
			change(&b)
			w := httptest.NewRecorder()
			writeICAReadiness(w, []endpointServer{&attestedICAServer{state: a}, &attestedICAServer{state: b}}, 0)
			if w.Code != 503 {
				t.Fatalf("unhealthy later snapshot accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
