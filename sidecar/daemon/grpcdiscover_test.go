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
	if err == nil || !strings.Contains(err.Error(), "needs a grpc or spanner listener") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverGRPCNamesTheGRPCLanesOnAMiss(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "pg", Protocol: "postgres", Listen: "127.0.0.1:5432", Upstream: "db:5432"},
		{Name: "api", Protocol: "grpc", Listen: "127.0.0.1:8443", Upstream: "svc:443"},
		{Protocol: "grpc", Listen: "127.0.0.1:8444", Upstream: "svc2:443"},
		{Name: "sp", Protocol: "spanner", Listen: "127.0.0.1:8445", Upstream: "spanner:443"},
	}}

	err := DiscoverGRPC(context.Background(), cfg, "nope", "", &strings.Builder{})
	if err == nil {
		t.Fatal("a wrong lane name was accepted")
	}
	// The candidates listed are grpc-transport lanes only — grpc AND
	// spanner, since a spanner lane's descriptors are fetched the same way
	// — under the same names the rest of the daemon reports: Name when
	// set, listener[i] when not.
	if !strings.Contains(err.Error(), "api, listener[2], sp") || strings.Contains(err.Error(), "pg") {
		t.Fatalf("err = %v", err)
	}
}

// Two grpc-transport lanes sharing a name is a config the loader accepts
// (their listen addresses differ), but discovery must not resolve it by
// slice order: the operator is fetching descriptors to PIN, and pinning the
// wrong upstream's schema is worse than no answer. The error must name both
// upstreams so the operator sees exactly what collided.
func TestDiscoverGRPCRefusesAmbiguousLaneNames(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "api", Protocol: "grpc", Listen: "127.0.0.1:8443", Upstream: "svc-a:443"},
		{Name: "api", Protocol: "spanner", Listen: "127.0.0.1:8444", Upstream: "svc-b:443"},
	}}

	err := DiscoverGRPC(context.Background(), cfg, "api", "", &strings.Builder{})
	if err == nil {
		t.Fatal("a name carried by two lanes was resolved instead of refused")
	}
	if !strings.Contains(err.Error(), "svc-a:443") || !strings.Contains(err.Error(), "svc-b:443") {
		t.Fatalf("the error does not name both candidate upstreams: %v", err)
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
