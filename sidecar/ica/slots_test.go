package ica

import (
	"context"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/libhoop-ica/ica"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestBuildSlotLaneWithoutModelsOrDNS(t *testing.T) {
	lane := daemon.ICALane{Name: "a", Listen: "127.0.0.1:21001", TenantID: "test", SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, Slots: &daemon.ICASlotsConfig{FirstPort: 21001, Count: 2, Names: []string{"vm702.lab.local", "vm703.lab.local"}, UpstreamPort: 2598, FrontendCIDR: "10.240.16.0/24", UpstreamCIDRs: []string{"10.77.0.0/24"}}}
	srv, e := Build(daemon.ICARuntime{}, lane)
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	lane.Slots.Names[0] = "changed.lab.local"
	if srv.(*server).lane.Slots.Names[0] != "vm702.lab.local" {
		t.Fatal("Build retained a mutable slot map")
	}
	if srv.Ready() {
		t.Fatal("ready before loading")
	}
	lane.EDT = true
	if _, e := Build(daemon.ICARuntime{}, lane); e == nil {
		t.Fatal("EDT slots accepted")
	}
}

func TestSignalDrainsExistingTCPBeforeRuntimeClose(t *testing.T) {
	previous := newRuntime
	newRuntime = func(ctx context.Context, _ ica.RuntimeConfig) (*ica.Runtime, error) {
		return ica.NewRuntime(ctx, ica.RuntimeConfig{BypassGraphics: true})
	}
	t.Cleanup(func() { newRuntime = previous })
	upstream, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer upstream.Close()
	srv, e := Build(daemon.ICARuntime{DrainTimeoutSec: 2}, daemon.ICALane{Name: "drain", TenantID: "test", Listen: "127.0.0.1:0", Upstream: upstream.Addr().String()})
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if srv.Addr() == nil {
		select {
		case err := <-done:
			t.Fatal("serve failed", err)
		default:
			t.Fatal("not listening")
		}
	}
	client, e := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	upstream.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
	back, e := upstream.Accept()
	if e != nil {
		t.Fatal(e)
	}
	defer back.Close()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		active, _, _ := srv.Stats()
		if active == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	active, _, _ := srv.Stats()
	if active != 1 {
		t.Fatal("no active session")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatal("signal cancelled admitted session before drain", err)
	case <-time.After(40 * time.Millisecond):
	}
	if srv.Ready() {
		t.Fatal("draining sidecar is ready")
	}
	client.Close()
	back.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("drain did not finish when session ended")
	}
}
