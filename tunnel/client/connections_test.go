package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newConnectionsGateway serves a fixed /api/connections payload so the
// filtering logic can be exercised without a real gateway.
func newConnectionsGateway(t *testing.T, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connections", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, srv *httptest.Server, flags map[string]bool) map[string]string {
	t.Helper()
	conns, err := FetchConnections(context.Background(), FetchConnectionsOptions{
		APIBaseURL:   srv.URL,
		Token:        "tok",
		FeatureFlags: flags,
	})
	if err != nil {
		t.Fatalf("FetchConnections: %v", err)
	}
	out := make(map[string]string, len(conns))
	for _, c := range conns {
		out[c.Name] = c.SubType
	}
	return out
}

// The tunnel must offer exactly the subtypes it can actually carry.
// Listing one it cannot (or that the agent will refuse) hands the user a
// hostname and credentials that never work.
func TestFetchConnectionsFiltersToTunnelableSubtypes(t *testing.T) {
	srv := newConnectionsGateway(t, `[
		{"name":"pg","subtype":"postgres"},
		{"name":"my","subtype":"mysql"},
		{"name":"ms","subtype":"mssql"},
		{"name":"mongo","subtype":"mongodb"},
		{"name":"raw","subtype":"tcp"},
		{"name":"api","subtype":"httpproxy"},
		{"name":"box","subtype":"ssh"},
		{"name":"k8s","subtype":"kubernetes"},
		{"name":"desktop","subtype":"rdp"},
		{"name":"shell","subtype":"command-line"}
	]`)

	got := fetch(t, srv, map[string]bool{})

	for _, name := range []string{"pg", "my", "ms", "mongo", "raw", "api"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%q (%s) should be tunnelable but was filtered out", name, got[name])
		}
	}
	for _, name := range []string{"box", "k8s", "desktop", "shell"} {
		if _, ok := got[name]; ok {
			t.Errorf("%q is not tunnelable but was listed", name)
		}
	}
}

// Native Oracle is gated by beta.oracle_native. The agent's Oracle handler
// closes the session immediately when the flag is off, so listing an Oracle
// connection then would advertise a resource that can never connect.
func TestFetchConnectionsGatesOracleBehindItsFeatureFlag(t *testing.T) {
	srv := newConnectionsGateway(t, `[
		{"name":"pg","subtype":"postgres"},
		{"name":"ora","subtype":"oracledb"}
	]`)

	t.Run("flag on", func(t *testing.T) {
		got := fetch(t, srv, map[string]bool{"beta.oracle_native": true})
		if _, ok := got["ora"]; !ok {
			t.Error("oracle must be listed when beta.oracle_native is enabled")
		}
	})

	t.Run("flag off", func(t *testing.T) {
		got := fetch(t, srv, map[string]bool{"beta.oracle_native": false})
		if _, ok := got["ora"]; ok {
			t.Error("oracle must not be listed when beta.oracle_native is disabled")
		}
		if _, ok := got["pg"]; !ok {
			t.Error("ungated connections must still be listed")
		}
	})

	// Flags unknown (serverinfo unreachable): hide the gated subtype rather
	// than guess it is available.
	t.Run("flags unknown", func(t *testing.T) {
		got := fetch(t, srv, nil)
		if _, ok := got["ora"]; ok {
			t.Error("oracle must not be listed when the flag state is unknown")
		}
		if _, ok := got["pg"]; !ok {
			t.Error("ungated connections must still be listed when flags are unknown")
		}
	})
}

// A connection with no name cannot be resolved to a hostname; skip it rather
// than allocate an unreachable address.
func TestFetchConnectionsSkipsUnnamedEntries(t *testing.T) {
	srv := newConnectionsGateway(t, `[{"name":"","subtype":"postgres"},{"name":"pg","subtype":"postgres"}]`)
	got := fetch(t, srv, nil)
	if len(got) != 1 {
		t.Fatalf("want only the named connection, got %v", got)
	}
}
