package kit

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeTwin struct {
	seeded []string
	resets int
}

func (f *fakeTwin) Name() string { return "fake" }
func (f *fakeTwin) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /real/", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "real") })
}
func (f *fakeTwin) Seed(raw json.RawMessage) error {
	f.seeded = append(f.seeded, string(raw))
	return nil
}
func (f *fakeTwin) State() any { return map[string]any{"seeded": len(f.seeded)} }
func (f *fakeTwin) Reset()     { f.seeded = nil; f.resets++ }
func (f *fakeTwin) Actions(mux *http.ServeMux) {
	mux.HandleFunc("POST /_twin/act/poke", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "poked") })
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFaults(t *testing.T) {
	for _, tc := range []struct {
		name, fault string
		wantStatus  int
		wantBody    string
		minDelay    time.Duration
	}{
		{"429", `{"path":"/real/","status":429,"body":"slow down"}`, 429, "slow down", 0},
		{"5xx", `{"path":"/real/","status":503}`, 503, "", 0},
		{"latency then real answer", `{"path":"/real/","delay_ms":150}`, 200, "real", 150 * time.Millisecond},
		{"latency then status", `{"path":"/real/","delay_ms":50,"status":500}`, 500, "", 50 * time.Millisecond},
		{"other path untouched", `{"path":"/other","status":500}`, 200, "real", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(Handler(&fakeTwin{}))
			defer srv.Close()
			if code, body := do(t, srv, "POST", "/_twin/faults", tc.fault); code != http.StatusNoContent {
				t.Fatalf("add fault: %d %s", code, body)
			}
			start := time.Now()
			code, body := do(t, srv, "GET", "/real/x", "")
			if code != tc.wantStatus || body != tc.wantBody {
				t.Fatalf("got %d %q, want %d %q", code, body, tc.wantStatus, tc.wantBody)
			}
			if d := time.Since(start); d < tc.minDelay {
				t.Fatalf("answered in %s, want at least %s", d, tc.minDelay)
			}
		})
	}
}

func TestFaultTimesAndClear(t *testing.T) {
	twin := &fakeTwin{}
	srv := httptest.NewServer(Handler(twin))
	defer srv.Close()
	do(t, srv, "POST", "/_twin/seed", `{"a":1}`)
	do(t, srv, "POST", "/_twin/faults", `{"path":"/real/","status":500,"times":1}`)
	if code, _ := do(t, srv, "GET", "/real/x", ""); code != 500 {
		t.Fatalf("first request: got %d, want the fault", code)
	}
	if code, _ := do(t, srv, "GET", "/real/x", ""); code != 200 {
		t.Fatalf("second request: got %d, want the fault spent after times=1", code)
	}

	do(t, srv, "POST", "/_twin/faults", `{"path":"/real/","status":500}`)
	_, state := do(t, srv, "GET", "/_twin/state", "")
	if !strings.Contains(state, `"hits":1`) || !strings.Contains(state, `"twin":"fake"`) {
		t.Fatalf("state does not show the faults and their hits: %s", state)
	}
	if code, _ := do(t, srv, "DELETE", "/_twin/faults", ""); code != http.StatusNoContent {
		t.Fatalf("clear faults: %d", code)
	}
	if code, _ := do(t, srv, "GET", "/real/x", ""); code != 200 {
		t.Fatalf("after clear: got %d, want the real answer", code)
	}
	if len(twin.seeded) != 1 || twin.resets != 0 {
		t.Fatalf("clearing faults must keep the seeded state, seeded=%v resets=%d", twin.seeded, twin.resets)
	}

	do(t, srv, "POST", "/_twin/faults", `{"path":"/real/","status":500}`)
	if code, _ := do(t, srv, "POST", "/_twin/reset", ""); code != http.StatusNoContent {
		t.Fatalf("reset: %d", code)
	}
	if code, _ := do(t, srv, "GET", "/real/x", ""); code != 200 || twin.resets != 1 || len(twin.seeded) != 0 {
		t.Fatalf("reset must clear the faults and the twin: code=%d resets=%d seeded=%v", code, twin.resets, twin.seeded)
	}
}

func TestBadFaultsAreRefused(t *testing.T) {
	srv := httptest.NewServer(Handler(&fakeTwin{}))
	defer srv.Close()
	for _, f := range []string{
		`{"path":"/real/"}`,                    // neither status nor delay
		`{"path":"real","status":500}`,         // not a path
		`{"path":"/_twin/state","status":500}`, // the admin API is not the surface
		`{"path":"/real/","status":42}`,
		`{"path":"/real/","delay_ms":-1,"status":500}`,
		`not json`,
	} {
		if code, _ := do(t, srv, "POST", "/_twin/faults", f); code != http.StatusBadRequest {
			t.Errorf("fault %s: got %d, want 400", f, code)
		}
	}
}

func TestAdminRoutes(t *testing.T) {
	srv := httptest.NewServer(Handler(&fakeTwin{}))
	defer srv.Close()
	if code, body := do(t, srv, "GET", "/_twin/healthz", ""); code != 200 || body != "ok" {
		t.Fatalf("healthz: %d %q", code, body)
	}
	if code, body := do(t, srv, "POST", "/_twin/act/poke", ""); code != 200 || body != "poked" {
		t.Fatalf("an Actor's routes live under /_twin/act/: %d %q", code, body)
	}
}

func TestPKI(t *testing.T) {
	dir := t.TempDir()
	if err := WritePKI(dir, map[string][]string{"a": {"twin-a", "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	ca1, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePKI(dir, map[string][]string{"a": {"other"}}); err != nil {
		t.Fatal(err)
	}
	ca2, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if string(ca1) != string(ca2) {
		t.Fatal("a second WritePKI must keep the CA and its leaves")
	}

	// A client that trusts only the CA reaches the leaf by its hostname.
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "tls") }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca1)
	for _, name := range []string{"twin-a", "unknown-host"} {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name}}}
		resp, err := client.Get(srv.URL)
		if name == "unknown-host" {
			if err == nil {
				resp.Body.Close()
				t.Fatal("the leaf must not answer for a host it does not name")
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
	}

	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ca.Leaf(); err == nil {
		t.Fatal("a leaf with no host must fail")
	}
}
