package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/hoophq/hoop/common/envloader"
)

var DefaultClient = loadHttpClient()

func defaultTransportDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return dialer.DialContext
}

type HttpClient interface {
	Do(req *http.Request) (*http.Response, error)
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

func loadHttpClient() HttpClient {
	client := httpClient{http.DefaultClient, nil}
	var certPool *x509.CertPool
	tlsCa, err := envloader.GetEnv("HOOP_TLSCA")
	if err != nil {
		client.err = err
		return &client
	}
	if tlsCa != "" {
		certPool = x509.NewCertPool()
		if !certPool.AppendCertsFromPEM([]byte(tlsCa)) {
			client.err = fmt.Errorf("unable to load HOOP_TLSCA: failed to append root CA into cert pool")
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
				RootCAs: certPool,
			},
		}
	}
	return &client
}
