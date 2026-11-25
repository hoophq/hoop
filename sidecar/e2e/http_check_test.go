//go:build integration

package e2e_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const httpToken = "Bearer e2e-tok-0123456789"

type orderRequest struct{ method, path, marker string }

// orderAPI serves order 1 as open and order 2 as closed. A GET echoes the
// caller's Authorization, as a careless API would.
type orderAPI struct {
	mu   sync.Mutex
	seen []orderRequest
	addr string
}

var orders = map[string]string{
	"/orders/1": `{"id":1,"status":"open","payment":"captured","shipment":"in_transit","total":1250.00,"items":3}`,
	"/orders/2": `{"id":2,"status":"closed","closed_at":"2025-01-03","archived":true,"items":0}`,
}

func startOrderAPI(t *testing.T) *orderAPI {
	t.Helper()
	api := &orderAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.seen = append(api.seen, orderRequest{r.Method, r.URL.Path, r.Header.Get("Hoop-Resource-Check")})
		api.mu.Unlock()
		body, ok := orders[r.URL.Path]
		switch {
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusNoContent)
		case !ok:
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, strings.TrimSuffix(body, "}")+`,"seen_auth":"`+r.Header.Get("Authorization")+`"}`)
		}
	}))
	t.Cleanup(srv.Close)
	api.addr = strings.TrimPrefix(srv.URL, "http://")
	return api
}

func (a *orderAPI) requests() []orderRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]orderRequest(nil), a.seen...)
}

func httpCheckConfig(t *testing.T, analyzer map[string]any, actions map[string]any, check bool) string {
	t.Helper()
	lane := map[string]any{"message": "blocked by the analyzer", "resource_check": check}
	for k, v := range actions {
		lane[k] = v
	}
	b, err := json.Marshal(map[string]any{
		"log_level": "debug",
		"audit":     map[string]any{"file": "-"},
		"analyzer":  analyzer,
		"listeners": []any{map[string]any{
			"name": "orders", "protocol": "http", "listen": "{{listen}}", "upstream": "{{upstream}}",
			"analyzer": lane,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// send writes one request on a new connection and returns the status.
func send(t *testing.T, addr, method, path string) int {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))
	req := method + " " + path + " HTTP/1.1\r\nHost: shop\r\nAuthorization: " + httpToken + "\r\nContent-Length: 0\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// riskOf returns the risk level the audit trail recorded for statement.
func riskOf(t *testing.T, s *sidecar, statement string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, ev := range s.events {
			if ev.Kind == "statement" && ev.Statement == statement && ev.Metadata["risk_level"] != "" {
				s.mu.Unlock()
				return ev.Metadata["risk_level"]
			}
		}
		s.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no risk level for %q in the audit trail", statement)
	return ""
}

func TestHTTPResourceCheckReachesTheModel(t *testing.T) {
	api := startOrderAPI(t)
	model := startRiskModel(t, func(content string) bool { return strings.Contains(content, `"status":"open"`) })
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer := map[string]any{"provider": "openai", "model": "e2e", "endpoint": model.url, "credentials_file": cred, "fail_open": false}
	logs := &lockedBuffer{}
	s := startSidecar(t, api.addr, httpCheckConfig(t, analyzer, map[string]any{"high": "block", "low": "allow"}, true),
		func(l *sidecarLaunch) { l.stderr = logs })

	if code := send(t, s.addr, "DELETE", "/orders/1"); code != http.StatusForbidden {
		t.Errorf("DELETE of the open order got %d, want 403", code)
	}
	if code := send(t, s.addr, "DELETE", "/orders/2"); code != http.StatusNoContent {
		t.Errorf("DELETE of the closed order got %d, want 204", code)
	}
	if code := send(t, s.addr, "POST", "/orders"); code != http.StatusNoContent {
		t.Errorf("POST got %d, want 204", code)
	}

	want := []orderRequest{
		{"GET", "/orders/1", "1"},
		{"GET", "/orders/2", "1"}, {"DELETE", "/orders/2", ""},
		{"POST", "/orders", ""},
	}
	if got := api.requests(); len(got) != len(want) {
		t.Errorf("upstream got %+v, want %+v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("upstream request %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	}
	in := model.input(t, `"status":"open"`)
	if !strings.Contains(in, "Resource check (a GET of the same URL") {
		t.Errorf("model input lacks the check block:\n%s", in)
	}
	if strings.Contains(in, "e2e-tok-0123456789") {
		t.Errorf("the bearer token reached the model:\n%s", in)
	}
	model.mu.Lock()
	for _, in := range model.inputs {
		if strings.HasPrefix(in, "POST") && strings.Contains(in, "Resource check") {
			t.Errorf("the POST was checked:\n%s", in)
		}
	}
	model.mu.Unlock()
	if !strings.Contains(logs.String(), `"msg":"resource check"`) {
		t.Errorf("no resource check in the log")
	}
}

func TestHTTPResourceCheckWithARealModel(t *testing.T) {
	analyzer := realAnalyzer(t)
	api := startOrderAPI(t)
	observe := map[string]any{"high": "warn", "medium": "warn", "low": "allow"}

	checked := startSidecar(t, api.addr, httpCheckConfig(t, analyzer, observe, true))
	send(t, checked.addr, "DELETE", "/orders/1")
	send(t, checked.addr, "DELETE", "/orders/2")
	open, closed := riskOf(t, checked, "DELETE /orders/1"), riskOf(t, checked, "DELETE /orders/2")
	t.Logf("with the check: open order %s, closed order %s", open, closed)
	if open == closed {
		t.Errorf("the open and the closed order got the same risk level %q", open)
	}

	plain := startSidecar(t, api.addr, httpCheckConfig(t, analyzer, observe, false))
	send(t, plain.addr, "DELETE", "/orders/1")
	send(t, plain.addr, "DELETE", "/orders/2")
	t.Logf("without the check: open order %s, closed order %s",
		riskOf(t, plain, "DELETE /orders/1"), riskOf(t, plain, "DELETE /orders/2"))
}
