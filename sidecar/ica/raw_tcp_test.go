package ica

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	engine "github.com/hoophq/libhoop-ica/ica"
)

func TestRawTCPBuildFreezesAndRejectsConflictingModes(t *testing.T) {
	c := &daemon.ICARawTCPConfig{Enable: true}
	srv, err := Build(daemon.ICARuntime{RawTCP: c}, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c.Enable = false
	if !srv.(*server).rt.RawTCP.Enable {
		t.Fatal("mutable config retained")
	}
	c.Enable = true
	lane := singleLane(t)
	lane.EDT = true
	if _, err = Build(daemon.ICARuntime{RawTCP: c}, lane); err == nil {
		t.Fatal("raw EDT accepted")
	}
	if _, err = Build(daemon.ICARuntime{RawTCP: c, DiagnosticRecording: diagnosticFixture(t)}, singleLane(t)); err == nil {
		t.Fatal("raw recording accepted")
	}
	c.Enable = false
	if srv, err = Build(daemon.ICARuntime{RawTCP: c, DiagnosticRecording: diagnosticFixture(t)}, singleLane(t)); err != nil {
		t.Fatal("disabled toggle affected recording", err)
	}
	defer srv.Close()
}

func TestRawTCPSharedRuntimeLifecycle(t *testing.T) {
	saved := newRuntime
	newRuntime = func(context.Context, engine.RuntimeConfig) (*engine.Runtime, error) { return nil, nil }
	defer func() { newRuntime = saved }()
	cfg := resolveRuntimeConfig(daemon.ICARuntime{})
	a, err := acquireRuntime(context.Background(), cfg, "", 2, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := acquireRuntime(context.Background(), cfg, "", 2, nil, true)
	if err != nil {
		releaseRuntime(a)
		t.Fatal(err)
	}
	if a != b || a.rawTCP == nil || a.rawTCP != b.rawTCP || a.limit != b.limit {
		t.Fatal("raw lanes did not share controller/capacity")
	}
	f := a.rawTCP
	if _, err = acquireRuntime(context.Background(), cfg, "", 2, nil, false); err == nil {
		t.Fatal("live raw disable accepted")
	}
	if _, err = acquireRuntime(context.Background(), cfg, "", 3, nil, true); err == nil {
		t.Fatal("live runtime change accepted")
	}
	if _, err = acquireRuntime(context.Background(), cfg, "", 2, diagnosticFixture(t), true); err == nil {
		t.Fatal("raw plus recording accepted")
	}
	if err = releaseRuntime(a); err != nil {
		t.Fatal(err)
	}
	if !f.Status().Enable {
		t.Fatal("one lane closed shared raw controller")
	}
	if err = releaseRuntime(b); err != nil {
		t.Fatal(err)
	}
	if f.Status().Enable {
		t.Fatal("last lane left raw controller enabled")
	}
	inspected, err := acquireRuntime(context.Background(), cfg, "", 2, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRuntime(inspected)
	if inspected.rawTCP != nil {
		t.Fatal("inspection inherited raw controller")
	}
	if _, err = acquireRuntime(context.Background(), cfg, "", 2, nil, true); err == nil {
		t.Fatal("live raw enable over inspected runtime accepted")
	}
}

func TestRawTCPConcurrentForwardingCapacityStatusAndShutdown(t *testing.T) {
	saved := newRuntime
	newRuntime = func(context.Context, engine.RuntimeConfig) (*engine.Runtime, error) { return nil, nil }
	defer func() { newRuntime = saved }()
	shared, err := acquireRuntime(context.Background(), resolveRuntimeConfig(daemon.ICARuntime{}), "", 2, nil, true)
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
	go func() {
		for {
			conn, e := vda.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	manager, err := engine.NewManager(ctx, engine.ManagerConfig{Runtime: inspectingMetadataRuntime{}, RawTCPForwarder: shared.rawTCP, SessionLimit: shared.limit, MaxSessions: 2, NetworkPolicy: engine.NetworkPolicy{Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}, DisableAuditOutput: true})
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
	srv := &server{rt: daemon.ICARuntime{RawTCP: &daemon.ICARawTCPConfig{Enable: true}, MaxSessions: 2, ReadinessCapacity: true, DrainTimeoutSec: 30}, manager: manager, tcp: front, shared: shared, sessionCancel: cancel}
	defer srv.Close()
	serving := make(chan error, 1)
	go func() {
		serving <- manager.ServeTransparent(front, engine.TransparentConfig{Mode: engine.ModeFixedUpstream, TenantID: "test", SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, ResolveDestination: func(net.Conn) (string, error) { return vda.Addr().String(), nil }})
	}()
	clients := make([]net.Conn, 0, 2)
	for range 2 {
		c, e := net.Dial("tcp", front.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		clients = append(clients, c)
		c.SetDeadline(time.Now().Add(3 * time.Second))
		payload := []byte("synthetic non-ICA bytes, not a valid handshake")
		if _, e = c.Write(payload); e != nil {
			t.Fatal(e)
		}
		got := make([]byte, len(payload))
		if _, e = io.ReadFull(c, got); e != nil {
			t.Fatal(e)
		}
		if string(got) != string(payload) {
			t.Fatal("raw bytes changed")
		}
	}
	s := srv.ICAInspectionState()
	if s.Ready || s.InspectionEnabled || s.ProtocolValidationEnabled || !s.GraphicsBypassed || s.RawTCP == nil || s.RawTCP.CaptureEnabled || s.RawTCP.Active != 2 {
		t.Fatal("capacity/mode status incorrect", s)
	}
	engineJSON, _ := json.Marshal(shared.rawTCP.Status())
	adapterJSON, _ := json.Marshal(s.RawTCP)
	var engineStatus, adapterStatus map[string]any
	json.Unmarshal(engineJSON, &engineStatus)
	json.Unmarshal(adapterJSON, &adapterStatus)
	if !reflect.DeepEqual(engineStatus, adapterStatus) {
		t.Fatal("raw status JSON drift", string(engineJSON), string(adapterJSON))
	}
	third, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	third.SetDeadline(time.Now().Add(3 * time.Second))
	third.Write([]byte("exceeds capacity"))
	var buf [1]byte
	if _, err = third.Read(buf[:]); err == nil {
		t.Fatal("third connection exceeded normal max_sessions")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("capacity rejection timed out", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("raw shutdown waited for normal 30s drain")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raw shutdown did not finish")
	}
	if err = <-serving; err != nil {
		t.Fatal(err)
	}
}
