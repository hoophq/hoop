package daemon

import (
	"net/http/httptest"
	"strings"
	"testing"
)

type drainableICA struct {
	fakeICAServer
	calls       int
	initialized bool
}

func (s *drainableICA) BeginICADrain() bool { s.calls++; return s.initialized }
func (s *drainableICA) ICAInspectionState() ICAInspectionState {
	return ICAInspectionState{Ready: s.calls == 0, InspectionEnabled: true, ProtocolValidationEnabled: true}
}

func TestICADeploymentDrainAuthenticationAndReadiness(t *testing.T) {
	secret := strings.Repeat("s", 32)
	for _, tc := range []struct {
		token, auth string
		code        int
	}{
		{"", secret, 403}, {"short", secret, 403}, {secret, "", 401}, {secret, "wrong", 401}, {secret, secret, 200},
	} {
		srv := &drainableICA{initialized: true}
		req := httptest.NewRequest("POST", "/v1/drain", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", "Bearer "+tc.auth)
		}
		w := httptest.NewRecorder()
		icaDrainHandler(tc.token, []endpointServer{srv}, 0)(w, req)
		if w.Code != tc.code {
			t.Fatalf("HTTP %d, want %d", w.Code, tc.code)
		}
		if tc.code != 200 && srv.calls != 0 {
			t.Fatal("unauthorized request drained admissions")
		}
		if tc.code == 200 {
			ready := httptest.NewRecorder()
			writeICAReadiness(ready, []endpointServer{srv}, 0)
			if ready.Code != 503 {
				t.Fatal("draining sidecar advertised ready")
			}
		}
	}
}

func TestICADeploymentDrainRefusesMixedAndUnsupportedEndpoints(t *testing.T) {
	secret := strings.Repeat("s", 32)
	for _, tc := range []struct {
		endpoints []endpointServer
		proxies   int
	}{
		{nil, 0}, {[]endpointServer{&fakeICAServer{}}, 0}, {[]endpointServer{&drainableICA{}}, 1},
		{[]endpointServer{&drainableICA{}, &fakeICAServer{}}, 0},
	} {
		req := httptest.NewRequest("POST", "/v1/drain", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		icaDrainHandler(secret, tc.endpoints, tc.proxies)(w, req)
		if w.Code != 409 {
			t.Fatalf("unsupported drain HTTP %d", w.Code)
		}
		for _, ep := range tc.endpoints {
			if srv, ok := ep.(*drainableICA); ok && srv.calls != 0 {
				t.Fatal("partial drain on unsupported process")
			}
		}
	}
}
