package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeService implements Service with hand-set return values so tests
// can drive the HTTP layer without standing up the real daemon.
type fakeService struct {
	mu sync.Mutex

	statusResp    StatusResponse
	statusErr     error
	connsResp     []Connection
	connsErr      error
	loginStart    LoginStartResponse
	loginStartErr error
	loginPoll     LoginPollResponse
	loginPollErr  error
	loginLocalGot LoginLocalRequest
	loginLocalErr error
	logoutErr     error
	configResp    ConfigResponse
	configErr     error
	updateResp    ConfigResponse
	updateErr     error
	updateGot     ConfigUpdateRequest
	reconnectErr  error
	reconnectHits int
}

func (f *fakeService) Status(context.Context) (StatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusResp, f.statusErr
}

func (f *fakeService) Connections(context.Context) ([]Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connsResp, f.connsErr
}

func (f *fakeService) LoginStart(context.Context) (LoginStartResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginStart, f.loginStartErr
}

func (f *fakeService) LoginPoll(_ context.Context, _ string) (LoginPollResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginPoll, f.loginPollErr
}

func (f *fakeService) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logoutErr
}

func (f *fakeService) LoginLocal(_ context.Context, req LoginLocalRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loginLocalGot = req
	return f.loginLocalErr
}

func (f *fakeService) Config(context.Context) (ConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configResp, f.configErr
}

func (f *fakeService) UpdateConfig(_ context.Context, req ConfigUpdateRequest) (ConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateGot = req
	return f.updateResp, f.updateErr
}

func (f *fakeService) Reconnect(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnectHits++
	return f.reconnectErr
}

// newTestServer builds a Server backed by fakeService and a fresh
// MemoryTokenStore, plus a discard-logger so test output stays clean.
// Returns the server, the fake, and the bearer token.
func newTestServer(t *testing.T, svc *fakeService) (*Server, string) {
	t.Helper()
	var store MemoryTokenStore
	tok, err := store.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	srv, err := NewServer(ServerOptions{
		Service:    svc,
		TokenStore: &store,
		Logger:     log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, tok.Raw()
}

// doReq sends a request through the server's in-process handler and
// returns the response recorder. Bearer auth is applied automatically;
// pass "" to skip and exercise the unauthorised path.
func doReq(t *testing.T, srv *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestServer_StatusHappyPath(t *testing.T) {
	svc := &fakeService{
		statusResp: StatusResponse{
			Running:       true,
			LoggedIn:      false,
			LastError:     "",
			DaemonVersion: "1.2.3",
		},
	}
	srv, tok := newTestServer(t, svc)

	rec := doReq(t, srv, tok, http.MethodGet, "/v1/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var got StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Running || got.LoggedIn || got.DaemonVersion != "1.2.3" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestServer_UnauthorizedWithoutBearer(t *testing.T) {
	srv, _ := newTestServer(t, &fakeService{})
	rec := doReq(t, srv, "", http.MethodGet, "/v1/status", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestServer_UnknownRouteReturnsJSON404(t *testing.T) {
	srv, tok := newTestServer(t, &fakeService{})
	rec := doReq(t, srv, tok, http.MethodGet, "/v1/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var e ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Code != "not_found" {
		t.Errorf("code = %q, want not_found", e.Code)
	}
}

func TestServer_ConnectionsEmptyReturnsArrayNotNull(t *testing.T) {
	// Even when the daemon returns a nil slice, the wire response must
	// be `{"connections":[]}`, not `{"connections":null}`. The TS
	// client depends on the empty-array shape.
	srv, tok := newTestServer(t, &fakeService{connsResp: nil})
	rec := doReq(t, srv, tok, http.MethodGet, "/v1/connections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"connections":[]`) {
		t.Errorf("response body = %q, expected empty array literal", rec.Body.String())
	}
}

func TestServer_ConnectionsHappyPath(t *testing.T) {
	want := []Connection{
		{Name: "pg-prod", SubType: "postgres", VirtualIP: "fd00::1", ExpectedPort: 5432},
		{Name: "tcp-foo", SubType: "tcp", VirtualIP: "fd00::2", ExpectedPort: 0},
	}
	svc := &fakeService{connsResp: want}
	srv, tok := newTestServer(t, svc)

	rec := doReq(t, srv, tok, http.MethodGet, "/v1/connections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp ConnectionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Connections) != len(want) {
		t.Fatalf("got %d connections, want %d", len(resp.Connections), len(want))
	}
	for i, c := range resp.Connections {
		if c != want[i] {
			t.Errorf("connections[%d] = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestServer_NotImplementedTranslatesTo501(t *testing.T) {
	svc := &fakeService{loginStartErr: ErrNotImplemented}
	srv, tok := newTestServer(t, svc)

	rec := doReq(t, srv, tok, http.MethodPost, "/v1/login/start", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%q", rec.Code, rec.Body.String())
	}
	var e ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != "not_implemented" {
		t.Errorf("code = %q, want not_implemented", e.Code)
	}
}

func TestServer_LoginPollRequiresState(t *testing.T) {
	srv, tok := newTestServer(t, &fakeService{})
	rec := doReq(t, srv, tok, http.MethodGet, "/v1/login/poll", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var e ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", e.Code)
	}
}

func TestServer_LoginPollPassesStateThrough(t *testing.T) {
	svc := &fakeService{loginPoll: LoginPollResponse{Status: LoginStatusPending}}
	srv, tok := newTestServer(t, svc)
	rec := doReq(t, srv, tok, http.MethodGet, "/v1/login/poll?state=abc-123", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var got LoginPollResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != LoginStatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
}

func TestServer_LoginLocalHappyPath(t *testing.T) {
	svc := &fakeService{}
	srv, tok := newTestServer(t, svc)

	rec := doReq(t, srv, tok, http.MethodPost, "/v1/login/local", LoginLocalRequest{
		Email:    "alice@example.com",
		Password: "hunter2",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if svc.loginLocalGot.Email != "alice@example.com" {
		t.Errorf("Email passed = %q", svc.loginLocalGot.Email)
	}
	if svc.loginLocalGot.Password != "hunter2" {
		t.Errorf("Password not propagated")
	}
}

func TestServer_LoginLocalValidatesFields(t *testing.T) {
	srv, tok := newTestServer(t, &fakeService{})
	rec := doReq(t, srv, tok, http.MethodPost, "/v1/login/local", LoginLocalRequest{
		Email:    "",
		Password: "x",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing email: status = %d, want 400", rec.Code)
	}
}

func TestServer_LogoutReturns204(t *testing.T) {
	srv, tok := newTestServer(t, &fakeService{})
	rec := doReq(t, srv, tok, http.MethodPost, "/v1/logout", nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestServer_ConfigPutInvalidJSON(t *testing.T) {
	srv, tok := newTestServer(t, &fakeService{})
	req := httptest.NewRequest(http.MethodPut, "/v1/config", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestServer_ConfigPutPassesFieldsThrough(t *testing.T) {
	want := ConfigResponse{APIURL: "https://hoop.example.com", LogLevel: "debug"}
	svc := &fakeService{updateResp: want}
	srv, tok := newTestServer(t, svc)

	apiURL := "https://hoop.example.com"
	level := "debug"
	rec := doReq(t, srv, tok, http.MethodPut, "/v1/config", ConfigUpdateRequest{
		APIURL:   &apiURL,
		LogLevel: &level,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	if svc.updateGot.APIURL == nil || *svc.updateGot.APIURL != apiURL {
		t.Errorf("APIURL passed = %v, want %s", svc.updateGot.APIURL, apiURL)
	}
	if svc.updateGot.LogLevel == nil || *svc.updateGot.LogLevel != level {
		t.Errorf("LogLevel passed = %v, want %s", svc.updateGot.LogLevel, level)
	}
}

func TestServer_ReconnectReturns202(t *testing.T) {
	svc := &fakeService{}
	srv, tok := newTestServer(t, svc)
	rec := doReq(t, srv, tok, http.MethodPost, "/v1/reconnect", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if svc.reconnectHits != 1 {
		t.Errorf("Reconnect called %d times, want 1", svc.reconnectHits)
	}
}

// TestNewServer_RequiresService catches the wiring error: forgetting to
// pass a Service must not produce a nil-deref at request time.
func TestNewServer_RequiresService(t *testing.T) {
	_, err := NewServer(ServerOptions{TokenStore: &MemoryTokenStore{}})
	if err == nil {
		t.Fatal("NewServer succeeded without Service")
	}
}

func TestNewServer_RequiresTokenStore(t *testing.T) {
	_, err := NewServer(ServerOptions{Service: &fakeService{}})
	if err == nil {
		t.Fatal("NewServer succeeded without TokenStore")
	}
}
