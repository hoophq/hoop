package daemon

import (
	"context"
	"strings"
	"testing"
)

// The success path — a live reflection server, the artifact round-trip —
// lives with codecgrpc.Discover in libhoop, next to the reflection client.
// These tests cover what this module owns: picking the lane and refusing
// bad selections with an error the operator can act on.

func TestDiscoverGRPCRefusesConfigWithoutGRPCLanes(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "pg", Protocol: "postgres", Listen: "127.0.0.1:5432", Upstream: "db:5432"},
	}}

	err := DiscoverGRPC(context.Background(), cfg, "pg", "", &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "needs a grpc listener") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverGRPCNamesTheGRPCLanesOnAMiss(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "pg", Protocol: "postgres", Listen: "127.0.0.1:5432", Upstream: "db:5432"},
		{Name: "api", Protocol: "grpc", Listen: "127.0.0.1:8443", Upstream: "svc:443"},
		{Protocol: "grpc", Listen: "127.0.0.1:8444", Upstream: "svc2:443"},
	}}

	err := DiscoverGRPC(context.Background(), cfg, "nope", "", &strings.Builder{})
	if err == nil {
		t.Fatal("a wrong lane name was accepted")
	}
	// The candidates listed are grpc lanes only, under the same names the
	// rest of the daemon reports: Name when set, listener[i] when not.
	if !strings.Contains(err.Error(), "api, listener[2]") || strings.Contains(err.Error(), "pg") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverGRPCSurfacesUpstreamTLSErrors(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "api", Protocol: "grpc", Listen: "127.0.0.1:8443", Upstream: "svc:443",
			UpstreamTLS: &TLSConfig{CAFile: "/does/not/exist.pem"}},
	}}

	err := DiscoverGRPC(context.Background(), cfg, "api", "", &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "upstream_tls") {
		t.Fatalf("err = %v", err)
	}
}
