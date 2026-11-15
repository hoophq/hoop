package ica

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	engine "github.com/hoophq/libhoop-ica/ica"
)

func diagnosticFixture(t *testing.T) *daemon.ICADiagnosticRecordingConfig {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	return &daemon.ICADiagnosticRecordingConfig{Until: time.Now().Add(time.Minute).UTC().Format(time.RFC3339), OutputDir: filepath.Join(parent, "capture"), AcknowledgeUnredacted: true}
}
func TestDiagnosticBuildIsInertAndFreezesConfiguration(t *testing.T) {
	c := diagnosticFixture(t)
	rt := daemon.ICARuntime{DiagnosticRecording: c}
	srv, err := Build(rt, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if _, err = os.Stat(c.OutputDir); !os.IsNotExist(err) {
		t.Fatal("Build created capture files", err)
	}
	original := c.Until
	c.Until = "mutated"
	if srv.(*server).rt.DiagnosticRecording.Until != original {
		t.Fatal("mutable recording config retained")
	}
	c.Until = original
	rt.DiagnosticRecording.AcknowledgeUnredacted = false
	if _, err = Build(rt, singleLane(t)); err == nil {
		t.Fatal("unacknowledged recording built")
	}
	rt.DiagnosticRecording.AcknowledgeUnredacted = true
	lane := singleLane(t)
	lane.EDT = true
	if _, err = Build(rt, lane); err == nil {
		t.Fatal("EDT recording built")
	}
}
func TestDiagnosticRecorderSharedAcrossLanesAndClosedAtLastRelease(t *testing.T) {
	saved := newRuntime
	newRuntime = func(context.Context, engine.RuntimeConfig) (*engine.Runtime, error) { return nil, nil }
	defer func() { newRuntime = saved }()
	c := diagnosticFixture(t)
	cfg := resolveRuntimeConfig(daemon.ICARuntime{})
	a, err := acquireRuntime(context.Background(), cfg, "", 32, c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := acquireRuntime(context.Background(), cfg, "", 32, c)
	if err != nil {
		releaseRuntime(a)
		t.Fatal(err)
	}
	if a != b || a.recorder == nil || a.recorder != b.recorder {
		t.Fatal("lanes did not share recorder")
	}
	recorder := a.recorder
	// No simultaneous normal/raw or differing raw runtime can be introduced.
	if _, err = acquireRuntime(context.Background(), cfg, "", 32, nil); err == nil {
		t.Fatal("live recording disable accepted")
	}
	other := *c
	other.Until = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	if _, err = acquireRuntime(context.Background(), cfg, "", 32, &other); err == nil {
		t.Fatal("live recording extension accepted")
	}
	if err = releaseRuntime(a); err != nil {
		t.Fatal(err)
	}
	if !recorder.Status().CaptureEnabled {
		t.Fatal("first lane closed shared recorder")
	}
	if err = releaseRuntime(b); err != nil {
		t.Fatal(err)
	}
	if recorder.Status().CaptureEnabled {
		t.Fatal("last release left recorder active")
	}
	// Fresh inspected runtime remains available once recording lanes close.
	normal, err := acquireRuntime(context.Background(), cfg, "", 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRuntime(normal)
	if _, err = acquireRuntime(context.Background(), cfg, "", 32, c); err == nil {
		t.Fatal("live raw enable over inspecting runtime accepted")
	}
}
func TestExpiredDiagnosticStartupDoesNotCreateFiles(t *testing.T) {
	saved := newRuntime
	newRuntime = func(context.Context, engine.RuntimeConfig) (*engine.Runtime, error) { return nil, nil }
	defer func() { newRuntime = saved }()
	c := diagnosticFixture(t)
	c.Until = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	shared, err := acquireRuntime(context.Background(), resolveRuntimeConfig(daemon.ICARuntime{}), "", 32, c)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRuntime(shared)
	if shared.recorder.Status().Mode != "inspection" {
		t.Fatal("expired recording enabled")
	}
	if _, err = os.Stat(c.OutputDir); !os.IsNotExist(err) {
		t.Fatal("expired recording touched capture directory", err)
	}
}

// Embedding retains the engine's ProcessorFactory signature without importing
// its internal packages. No processor is built in this metadata-only test.
type inspectingMetadataRuntime struct{ *engine.Runtime }

func (inspectingMetadataRuntime) Ready() bool             { return true }
func (inspectingMetadataRuntime) InspectionEnabled() bool { return true }
func (inspectingMetadataRuntime) GraphicsBypassed() bool  { return false }
func TestAdapterReportsActualRecordingAndRestoredInspection(t *testing.T) {
	c := diagnosticFixture(t)
	config, err := diagnosticConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := engine.NewDiagnosticRecorder(*config)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	manager, err := engine.NewManager(context.Background(), engine.ManagerConfig{Runtime: inspectingMetadataRuntime{}, DiagnosticRecorder: recorder, NetworkPolicy: engine.NetworkPolicy{Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	srv := &server{rt: daemon.ICARuntime{DiagnosticRecording: c}, manager: manager}
	s := srv.ICAInspectionState()
	if !s.Ready || s.InspectionEnabled || s.ProtocolValidationEnabled || !s.GraphicsBypassed || s.DiagnosticRecording == nil || !s.DiagnosticRecording.CaptureEnabled {
		t.Fatal("false raw attestation", s)
	}
	engineJSON, _ := json.Marshal(recorder.Status())
	adapterJSON, _ := json.Marshal(s.DiagnosticRecording)
	var engineStatus, adapterStatus map[string]any
	json.Unmarshal(engineJSON, &engineStatus)
	json.Unmarshal(adapterJSON, &adapterStatus)
	if !reflect.DeepEqual(engineStatus, adapterStatus) {
		t.Fatal("relay recording JSON drifted from engine", string(engineJSON), string(adapterJSON))
	}
	recorder.Close()
	s = srv.ICAInspectionState()
	if !s.Ready || !s.InspectionEnabled || !s.ProtocolValidationEnabled || s.GraphicsBypassed || s.DiagnosticRecording.Mode != "inspection" {
		t.Fatal("inspection not restored", s)
	}
}

func TestDiagnosticShutdownCancelsRawSessionBeforeNormalDrainGrace(t *testing.T) {
	saved := newRuntime
	newRuntime = func(context.Context, engine.RuntimeConfig) (*engine.Runtime, error) { return nil, nil }
	defer func() { newRuntime = saved }()
	c := diagnosticFixture(t)
	shared, err := acquireRuntime(context.Background(), resolveRuntimeConfig(daemon.ICARuntime{}), "", 32, c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vda, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		releaseRuntime(shared)
		t.Fatal(err)
	}
	defer vda.Close()
	accepted := make(chan struct{})
	go func() {
		conn, e := vda.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		io.Copy(io.Discard, conn)
	}()
	manager, err := engine.NewManager(ctx, engine.ManagerConfig{Runtime: inspectingMetadataRuntime{}, DiagnosticRecorder: shared.recorder, NetworkPolicy: engine.NetworkPolicy{Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}, DisableAuditOutput: true})
	if err != nil {
		releaseRuntime(shared)
		t.Fatal(err)
	}
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		manager.Close()
		releaseRuntime(shared)
		t.Fatal(err)
	}
	srv := &server{rt: daemon.ICARuntime{DiagnosticRecording: c, DrainTimeoutSec: 30}, manager: manager, tcp: front, shared: shared, sessionCancel: cancel}
	defer srv.Close()
	serving := make(chan error, 1)
	go func() {
		serving <- manager.ServeTransparent(front, engine.TransparentConfig{Mode: engine.ModeFixedUpstream, TenantID: "test", SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, ResolveDestination: func(net.Conn) (string, error) { return vda.Addr().String(), nil }})
	}()
	client, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.Write([]byte("synthetic diagnostic test"))
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("raw session did not reach synthetic VDA")
	}
	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic shutdown waited for 30s inspection drain")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raw shutdown did not finish")
	}
	if err := <-serving; err != nil {
		t.Fatal(err)
	}
}
