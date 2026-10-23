package analyzer_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	"github.com/hoophq/hoop/sidecar/analyzer/gemini"
	"github.com/hoophq/hoop/sidecar/analyzer/hoop"
	"github.com/hoophq/hoop/sidecar/analyzer/openai"
)

// Behind an egress proxy that re-signs TLS, Options.HTTPClient is the only
// client that trusts the proxy's CA. A provider that kept its own client
// would fail verification on every statement. The stand-in's certificate is
// in the injected client's roots and nowhere else, so a provider built
// without it must be refused before the request reaches the server.
func TestProvidersSendThroughTheInjectedClient(t *testing.T) {
	for _, tc := range []struct {
		name, cred, reply string
	}{
		{anthropic.Name, "k", `{"content":[{"type":"tool_use","name":"report_low_risk","input":{}}],"stop_reason":"tool_use"}`},
		{openai.Name, "k", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"report_low_risk","arguments":"{}"}}]}}]}`},
		{gemini.Name, "k", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"report_low_risk","args":{}}}]},"finishReason":"STOP"}]}`},
		// hoop wraps the injected transport in a signer; it must not
		// replace it.
		{hoop.Name, "", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"report_low_risk","arguments":"{}"}}]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer srv.Close()

			roots := x509.NewCertPool()
			roots.AddCert(srv.Certificate())
			trusting := &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots},
			}}
			build := func(client *http.Client) analyzer.Provider {
				p, err := analyzer.NewProvider(tc.name, analyzer.Options{
					Model:      "m",
					Endpoint:   srv.URL,
					Credential: analyzer.NewSecret([]byte(tc.cred)),
					HTTPClient: client,
				})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}

			if _, err := build(nil).Classify(context.Background(), "s", "c"); err == nil ||
				!strings.Contains(err.Error(), "certificate") {
				t.Fatalf("Classify with no injected client = %v, want a certificate error", err)
			}
			if n := hits.Load(); n != 0 {
				t.Fatalf("server saw %d requests over an untrusting client", n)
			}

			res, err := build(trusting).Classify(context.Background(), "s", "c")
			if err != nil {
				t.Fatalf("Classify through the injected client: %v", err)
			}
			if res.RiskLevel != analyzer.RiskLow || hits.Load() != 1 {
				t.Errorf("risk %q after %d requests, want %q after 1", res.RiskLevel, hits.Load(), analyzer.RiskLow)
			}
		})
	}
}
