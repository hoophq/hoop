package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newRotationGateway is a fake gateway whose /api/serverinfo and
// /api/connections endpoints honour the X-New-Access-Token rotation
// contract: when rotateTo is non-empty the header is attached to
// successful responses, and requests carrying deadToken get a 401.
func newRotationGateway(t *testing.T, rotateTo, deadToken string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handler := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer "+deadToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if rotateTo != "" {
				w.Header().Set("X-New-Access-Token", rotateTo)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/api/serverinfo", handler(`{"grpc_url":"grpc://127.0.0.1:8010"}`))
	mux.HandleFunc("/api/connections", handler(`[{"name":"pg-prod","subtype":"postgres"}]`))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchServerInfoHarvestsRotatedToken(t *testing.T) {
	srv := newRotationGateway(t, "fresh-token", "")

	var got string
	si, err := FetchServerInfo(context.Background(), FetchServerInfoOptions{
		APIBaseURL: srv.URL,
		Token:      "old-token",
		OnNewToken: func(tok string) { got = tok },
	})
	if err != nil {
		t.Fatalf("FetchServerInfo: %v", err)
	}
	if si.GrpcURL == "" {
		t.Fatal("missing grpc_url")
	}
	if got != "fresh-token" {
		t.Fatalf("OnNewToken not invoked with the rotated token: got %q", got)
	}
}

func TestFetchConnectionsHarvestsRotatedToken(t *testing.T) {
	srv := newRotationGateway(t, "fresh-token", "")

	var got string
	conns, err := FetchConnections(context.Background(), FetchConnectionsOptions{
		APIBaseURL: srv.URL,
		Token:      "old-token",
		OnNewToken: func(tok string) { got = tok },
	})
	if err != nil {
		t.Fatalf("FetchConnections: %v", err)
	}
	if len(conns) != 1 || conns[0].Name != "pg-prod" {
		t.Fatalf("unexpected connections: %+v", conns)
	}
	if got != "fresh-token" {
		t.Fatalf("OnNewToken not invoked with the rotated token: got %q", got)
	}
}

func TestNoRotationHeaderMeansNoCallback(t *testing.T) {
	srv := newRotationGateway(t, "", "")

	called := false
	_, err := FetchServerInfo(context.Background(), FetchServerInfoOptions{
		APIBaseURL: srv.URL,
		Token:      "old-token",
		OnNewToken: func(string) { called = true },
	})
	if err != nil {
		t.Fatalf("FetchServerInfo: %v", err)
	}
	if called {
		t.Fatal("OnNewToken must not fire without the rotation header")
	}
}

func TestUnauthorizedIsTypedOnBothEndpoints(t *testing.T) {
	srv := newRotationGateway(t, "", "dead-token")

	_, err := FetchServerInfo(context.Background(), FetchServerInfoOptions{
		APIBaseURL: srv.URL,
		Token:      "dead-token",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("FetchServerInfo 401 must wrap ErrUnauthorized, got: %v", err)
	}

	_, err = FetchConnections(context.Background(), FetchConnectionsOptions{
		APIBaseURL: srv.URL,
		Token:      "dead-token",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("FetchConnections 401 must wrap ErrUnauthorized, got: %v", err)
	}
}
