package oidcprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestUserInfoEndpointTimesOut(t *testing.T) {
	prev := userInfoTimeout
	userInfoTimeout = 50 * time.Millisecond
	t.Cleanup(func() { userInfoTimeout = prev })

	// A userinfo endpoint that never answers. Cleanups run last-in first-out,
	// so stop releases the handler before srv.Close waits on it.
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })

	p := &Provider{oidcProvider: (&oidc.ProviderConfig{
		IssuerURL:   srv.URL,
		UserInfoURL: srv.URL + "/userinfo",
	}).NewProvider(context.Background())}

	done := make(chan error, 1)
	go func() {
		_, err := p.userInfoEndpoint("token")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from a stalled userinfo endpoint")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("userinfo call did not time out")
	}
}
