package otlp

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/telemetry"
)

// The three http exporters at the pinned versions disagree on what an empty
// URL path means, so the endpoint is split by hand: host to WithEndpoint,
// the signal's path under the URL's path to WithURLPath, and TLS off for
// an http:// scheme or insecure. A base URL with or without a trailing
// slash or a prefix must land on the same target.
func TestHTTPTargetSplitsTheEndpointURL(t *testing.T) {
	cases := []struct {
		endpoint     string
		insecure     bool
		host, path   string
		wantInsecure bool
	}{
		{"http://otel-collector:4318", false, "otel-collector:4318", "/v1/metrics", true},
		{"http://otel-collector:4318/", false, "otel-collector:4318", "/v1/metrics", true},
		{"https://otel.example.com", false, "otel.example.com", "/v1/metrics", false},
		{"https://otel.example.com/otlp/", false, "otel.example.com", "/otlp/v1/metrics", false},
		{"https://otel.example.com:443/tenant/a", true, "otel.example.com:443", "/tenant/a/v1/metrics", true},
	}
	for _, tc := range cases {
		cfg := &telemetry.Config{OTLP: telemetry.OTLPConfig{
			Endpoint: tc.endpoint, Protocol: telemetry.ProtocolHTTP, Insecure: tc.insecure,
		}}
		got := newHTTPTarget(cfg, "/v1/metrics")
		if got.host != tc.host || got.path != tc.path || got.insecure != tc.wantInsecure {
			t.Errorf("%s insecure=%v: got host=%q path=%q insecure=%v, want %q %q %v",
				tc.endpoint, tc.insecure, got.host, got.path, got.insecure, tc.host, tc.path, tc.wantInsecure)
		}
	}
}
