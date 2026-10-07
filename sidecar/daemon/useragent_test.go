package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// recordingServer answers 200 and hands back the User-Agent of the last
// request it saw.
func recordingServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &ua
}

// A request from this process must be told from any other Go program in a
// provider's logs: Google's side asked for a hoop prefix instead of the
// runtime's Go-http-client. The component rides in the comment so one call
// path can be isolated, and the release is the hoop release, not a constant
// of this package.
func TestOutboundRequestsIdentifyTheSidecar(t *testing.T) {
	old := Version
	Version = "1.212.0"
	t.Cleanup(func() { Version = old })

	srv, ua := recordingServer(t)
	want := fmt.Sprintf("hoop-sidecar/1.212.0 (analyzer/vertex; %s/%s; %s)",
		runtime.GOOS, runtime.GOARCH, runtime.Version())

	for name, client := range map[string]*http.Client{
		"host trust store": outboundHTTPClient(nil, "analyzer/vertex"),
		"trust roots":      outboundHTTPClient(mustTrustRoots(t, newTestCA(t, "any").pemFile), "analyzer/vertex"),
	} {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = resp.Body.Close()
		if *ua != want {
			t.Errorf("%s: User-Agent = %q, want %q", name, *ua, want)
		}
	}
}

// A caller that set its own agent is not overridden: the transport fills a
// gap, it does not own the header.
func TestUserAgentTransportKeepsAnExplicitAgent(t *testing.T) {
	srv, ua := recordingServer(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "someone-else/1.0")
	resp, err := outboundHTTPClient(nil, "test").Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if *ua != "someone-else/1.0" {
		t.Errorf("User-Agent = %q, the caller's own was replaced", *ua)
	}
}

// The control plane reads the agent through the gateway's normalizer, which
// keeps the first product token: every sidecar call attributes to one
// caller there, whatever component made it.
func TestControlPlaneHandshakeCarriesTheUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Header().Set(LicenseManagedHeader, "true")
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(srv.Close)

	if _, err := fetchControlPlaneConfig(srv.URL, tokenCredential("hsc_token"), handshakeRequest{Version: Version}); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if want := userAgent("controlplane"); ua != want {
		t.Errorf("User-Agent = %q, want %q", ua, want)
	}
}
