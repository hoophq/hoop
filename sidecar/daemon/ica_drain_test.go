package daemon

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/proxy"
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
		icaDrainHandler(tc.token, testLaneSet([]endpointServer{srv}, 0))(w, req)
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
		icaDrainHandler(secret, testLaneSet(tc.endpoints, tc.proxies))(w, req)
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

// testLaneSet is a registry of the given endpoint lanes plus proxies relay
// lanes, each an unserved proxy.Server, for handlers that count both.
func testLaneSet(endpoints []endpointServer, proxies int) *laneSet {
	s := &laneSet{}
	for i, ep := range endpoints {
		s.lanes = append(s.lanes, runningLane{name: fmt.Sprintf("ep%d", i), endpoint: ep})
	}
	for i := range proxies {
		s.lanes = append(s.lanes, runningLane{name: fmt.Sprintf("relay%d", i), relay: &proxy.Server{}})
	}
	return s
}
