package daemon

import (
	"fmt"
	"net/http"
	"runtime"
)

// userAgentProduct is the product token every outbound HTTP request of this
// process carries, so a request from the sidecar can be told from any other
// Go program in a provider's logs by one prefix filter. Google's Cloud
// Logging records it under httpRequest.userAgent, and its audit logs under
// requestMetadata.callerSuppliedUserAgent.
const userAgentProduct = "hoop-sidecar"

// userAgent builds the User-Agent for one outbound client, in the shape
// RFC 9110 §10.1.5 gives it: a product token with the release, then a
// comment naming the component that made the call, the platform and the Go
// runtime.
//
//	hoop-sidecar/1.212.0 (analyzer/vertex; linux/amd64; go1.26.0)
//
// The component is a comment, not a second product, so the gateway's
// normalizer (the first product token) reads every sidecar call as one
// caller. Go's own token is left out rather than appended: it reads
// Go-http-client/1.1 or /2.0 by the protocol negotiated per request, and a
// fixed copy of it would lie under one of the two.
func userAgent(component string) string {
	return fmt.Sprintf("%s/%s (%s; %s/%s; %s)",
		userAgentProduct, Version, component, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// userAgentTransport sets the User-Agent on every request that carries none,
// then hands it on. It sits on the transport rather than on each request so
// the libraries this process calls through are covered too: oauth2 mints its
// tokens over the *http.Client it is handed (see analyzer/vertex and
// descriptors/gcs), and a per-request header would miss those exchanges.
//
// A request that already names an agent is sent as it is, so a caller that
// has a reason to identify itself differently can.
type userAgentTransport struct {
	next http.RoundTripper
	ua   string
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.next.RoundTrip(req)
	}
	// RoundTrip must not modify the request it was given.
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.ua)
	return t.next.RoundTrip(r)
}

// withUserAgent wraps a transport so its requests identify this process as
// component. A nil next is the default transport, as it is for http.Client.
func withUserAgent(next http.RoundTripper, component string) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return userAgentTransport{next: next, ua: userAgent(component)}
}
