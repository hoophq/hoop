package oidcprovider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

const jwtBearerAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// readClientAssertion reads the JWT at path. Kubernetes rotates projected
// service account tokens, so callers read it again on each token request.
func readClientAssertion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed reading client assertion file, reason=%v", err)
	}
	assertion := strings.TrimSpace(string(data))
	if assertion == "" {
		return "", fmt.Errorf("client assertion file %v is empty", path)
	}
	return assertion, nil
}

// clientAssertionTransport authenticates OAuth2 token requests with the JWT
// in file (RFC 7523, section 2.2). x/oauth2 cannot add parameters to a
// refresh request, so the transport adds them to each request it sends.
type clientAssertionTransport struct {
	file string
	base http.RoundTripper
}

func (t *clientAssertionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed reading token request body, reason=%v", err)
		}
	}
	if req.Method != http.MethodPost {
		return nil, fmt.Errorf("client assertion applies only to token requests, got method %v", req.Method)
	}
	assertion, err := readClientAssertion(t.file)
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("failed parsing token request body, reason=%v", err)
	}
	form.Set("client_assertion_type", jwtBearerAssertionType)
	form.Set("client_assertion", assertion)
	encoded := form.Encode()

	r := req.Clone(req.Context())
	r.Body = io.NopCloser(strings.NewReader(encoded))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(encoded)), nil }
	r.ContentLength = int64(len(encoded))
	return t.base.RoundTrip(r)
}

// withTokenClient returns ctx for a token endpoint request. With a client
// assertion file, the HTTP client in ctx sends the assertion in place of a secret.
func (p *Provider) withTokenClient(ctx context.Context) context.Context {
	if p.ClientAssertionFile == "" {
		return ctx
	}
	client := http.Client{}
	if c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && c != nil {
		client = *c
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &clientAssertionTransport{file: p.ClientAssertionFile, base: base}
	return context.WithValue(ctx, oauth2.HTTPClient, &client)
}
