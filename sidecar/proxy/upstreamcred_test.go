package proxy_test

import (
	"testing"

	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/upstreamcred/upstreamcredtest"
)

// gate.New refuses a codec that cannot send the lane's credential on every
// connection; the relay refuses it once, at startup, so a misbuilt lane
// does not start rather than failing every client.
func TestNewServerRefusesAnUpstreamCredentialItCannotSend(t *testing.T) {
	cred := upstreamcredtest.Open(t, "Authorization", "Bearer")
	httpFactory := func() inspect.Codec { return httpcodec.New(httpcodec.Options{}) }
	for name, cfg := range map[string]proxy.Config{
		"off http":   {Protocol: inspect.Postgres, CodecFactory: httpFactory},
		"no factory": {Protocol: inspect.HTTP},
		"a codec that lifts the same header": {Protocol: inspect.HTTP, CodecFactory: func() inspect.Codec {
			return httpcodec.New(httpcodec.Options{CredentialHeader: "authorization"})
		}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Listen, cfg.Upstream, cfg.UpstreamCredential = "127.0.0.1:0", "127.0.0.1:1", cred
			if _, err := proxy.NewServer(cfg); err == nil {
				t.Error("NewServer accepted it")
			}
		})
	}
	if _, err := proxy.NewServer(proxy.Config{Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1",
		Protocol: inspect.HTTP, CodecFactory: httpFactory, UpstreamCredential: cred}); err != nil {
		t.Errorf("NewServer refused an http lane: %v", err)
	}
}
