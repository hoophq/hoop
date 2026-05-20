package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func defaultTransportDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return dialer.DialContext
}

// VersionCheckCallback, when set by an importer (e.g. the CLI), is
// invoked with the value of the Server response header after every
// successful round-trip. It is used to warn users when the gateway and
// the local CLI are on different versions.
//
// Left as nil by default so server-to-server callers (gateway, agent)
// stay completely silent. The callback should be cheap and safe to call
// concurrently — implementations typically gate the actual warning with
// sync.Once.
var VersionCheckCallback func(serverHeader string)

type HttpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// versionCheckRT is an http.RoundTripper that delegates to base and
// forwards the response's Server header to VersionCheckCallback.
type versionCheckRT struct{ base http.RoundTripper }

func (v *versionCheckRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := v.base.RoundTrip(req)
	if resp != nil && VersionCheckCallback != nil {
		VersionCheckCallback(resp.Header.Get("Server"))
	}
	return resp, err
}

type httpClient struct {
	client *http.Client
	err    error
}

func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.client.Do(req)
}

func NewHttpClient(tlsCA string) HttpClient {
	client := httpClient{http.DefaultClient, nil}

	skipVerify := os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true"
	if skipVerify {
		client.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
		}
	}

	var certPool *x509.CertPool
	if tlsCA != "" {
		certPool = x509.NewCertPool()
		if !certPool.AppendCertsFromPEM([]byte(tlsCA)) {
			client.err = fmt.Errorf("failed to append root CA into cert pool")
			return &client
		}
		// from http.DefaultTransport
		client.client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: defaultTransportDialContext(&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig: &tls.Config{
				RootCAs:            certPool,
				InsecureSkipVerify: skipVerify,
			},
		}
	}

	// When an importer has registered VersionCheckCallback (the CLI does
	// this from its root command init), wrap the transport so every
	// response's Server header is forwarded. We rebuild the embedded
	// *http.Client so we never replace the transport on http.DefaultClient,
	// which is a process-wide singleton.
	if client.err == nil && VersionCheckCallback != nil {
		base := client.client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.client = &http.Client{Transport: &versionCheckRT{base: base}}
	}

	return &client
}
