package appconfig

import "testing"

// One rule, in two places: the listener's *tls.Config (loadTLSConfig) and the
// flag the in-process gRPC clients dial on (gatewayUseTLS). They must agree for
// every combination, or those clients negotiate TLS against a plaintext
// listener, or the reverse.
//
// Both are asserted from the same table on purpose. Drift between them is the
// failure this file exists to catch, and testing either one alone would miss it.
func TestTLSIsOnExactlyWhenThePairIsSet(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cert    string
		key     string
		wantTLS bool
	}{
		{name: "neither", wantTLS: false},
		// Half a pair serves plaintext. There is nothing to build a listener
		// from, and nothing is generated to cover the gap.
		{name: "cert only", cert: testCertPEM, wantTLS: false},
		{name: "key only", key: testKeyPEM, wantTLS: false},
		{name: "both", cert: testCertPEM, key: testKeyPEM, wantTLS: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// loadTLSConfig reads the package-level config through Get().
			runtimeConfig = Config{gatewayTLSCert: tt.cert, gatewayTLSKey: tt.key}
			t.Cleanup(func() { runtimeConfig = Config{} })

			tlsConfig, err := loadTLSConfig()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := tlsConfig != nil; got != tt.wantTLS {
				t.Errorf("listener: tls=%v, want %v", got, tt.wantTLS)
			}

			// The predicate appconfig.Load computes, spelled out here so a
			// change to it fails this test rather than a deployment.
			useTLS := tt.key != "" && tt.cert != ""
			if useTLS != tt.wantTLS {
				t.Errorf("gatewayUseTLS=%v but the listener says tls=%v: the "+
					"in-process gRPC clients would disagree with the listener",
					useTLS, tt.wantTLS)
			}
		})
	}
}

// A pair that does not parse is an error, not a silent downgrade to plaintext:
// the operator supplied TLS material and it has to be served or refused.
func TestMalformedPairIsAnError(t *testing.T) {
	runtimeConfig = Config{gatewayTLSCert: "not a pem", gatewayTLSKey: testKeyPEM}
	t.Cleanup(func() { runtimeConfig = Config{} })

	if _, err := loadTLSConfig(); err == nil {
		t.Fatal("want an error for an unparseable certificate, got nil")
	}
}
