package http_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func TestDotSegmentsCannotDodgeAResourceRule(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.WithResources("/admin/**"),
		policy.Rule{Name: "no-invoices", Type: policy.MatchHTTPResource}.WithResources("/billing/invoices"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		denied bool
	}{
		{"/admin/users", true},
		{"//admin/users", true},
		{"/admin//users", true},
		{"/./admin/users", true},
		{"/x/../admin/users", true},
		{"/%2e/admin/users", true},
		{"/x/%2E%2E/admin/users", true},
		{`/x\..\admin\users`, true},
		{"/admin/../public", true}, // an upstream that routes the raw path runs /admin
		{"/billing//invoices", true},
		{"/billing/./invoices", true},
		{"/public/users", false},
		{"/public/./users", false},
		{"/billing/invoices/../receipts", false},
	} {
		t.Run(tc.target, func(t *testing.T) {
			s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", tc.target))
			if got := rules.Evaluate(s).Denied; got != tc.denied {
				t.Errorf("denied = %v, want %v; resource %q, unresolved %q",
					got, tc.denied, s.HTTP.Resource, s.Metadata[inspect.MetadataHTTPResourceUnresolved])
			}
		})
	}
}

func TestResourceIsCleaned(t *testing.T) {
	for _, tc := range []struct {
		target, resource, unresolved string
	}{
		{"/./admin/users", "/admin/users", "/./admin/users"},
		{"/users/12345/../admin", "/users/admin", "/users/*/../admin"},
		{"/a/./b/", "/a/b/", "/a/./b/"},
		{"/..", "/", "/.."},
		{"//admin//users", "/admin/users", ""},
		{`/x\y`, "/x/y", ""},
		{"/admin/users/", "/admin/users/", ""},
		{"/", "/", ""},
	} {
		t.Run(tc.target, func(t *testing.T) {
			s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", tc.target))
			if s.HTTP.Resource != tc.resource {
				t.Errorf("Resource = %q, want %q", s.HTTP.Resource, tc.resource)
			}
			if want := []string{strings.ToLower(tc.resource)}; !slices.Equal(s.Tables, want) {
				t.Errorf("Tables = %q, want %q", s.Tables, want)
			}
			if got := s.Metadata[inspect.MetadataHTTPResourceUnresolved]; got != tc.unresolved {
				t.Errorf("unresolved = %q, want %q", got, tc.unresolved)
			}
			if s.HTTP.Target != tc.target {
				t.Errorf("Target = %q, want the request-target as sent", s.HTTP.Target)
			}
		})
	}
}

func TestAsteriskTargetIsNotCleaned(t *testing.T) {
	s := decodeRequest(t, codechttp.New(codechttp.Options{}), "OPTIONS * HTTP/1.1\r\nHost: api.example.com\r\n\r\n")
	if s.HTTP.Resource != "*" {
		t.Errorf("Resource = %q, want *", s.HTTP.Resource)
	}
}

func TestInspectRequestAndResponseCleanTheResource(t *testing.T) {
	c := codechttp.New(codechttp.Options{})
	r := httptest.NewRequest("GET", "http://api.example.com/x/../admin/users", nil)
	for name, s := range map[string]inspect.Statement{
		"request":  c.InspectRequest(r, nil),
		"response": c.InspectResponse(&http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Request: r}, r, nil),
	} {
		if s.HTTP.Resource != "/admin/users" {
			t.Errorf("%s: Resource = %q, want /admin/users", name, s.HTTP.Resource)
		}
	}
}

func TestDotSegmentsCannotDodgeAConnectGatewayRule(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-secrets", Type: policy.MatchHTTPResource}.
			WithResources("/api/v1/namespaces/*/secrets/**"),
	})
	if err != nil {
		t.Fatal(err)
	}
	const gw = "/v1/projects/p/locations/global/gkeMemberships/prod"
	for _, tc := range []struct{ target, unresolved string }{
		{gw + "/./api/v1/namespaces/kube-system/secrets/token", "/./api/v1/namespaces/kube-system/secrets/token"},
		{gw + "/api/v1/namespaces/kube-system/secrets/../configmaps", "/api/v1/namespaces/kube-system/secrets/../configmaps"},
		// The gateway routes the raw membership and the cluster resolves the rest.
		{gw + "/../api/v1/namespaces/kube-system/secrets/token", "/../api/v1/namespaces/kube-system/secrets/token"},
		{gw + "/../../api/v1/namespaces/kube-system/secrets/token", "/../../api/v1/namespaces/kube-system/secrets/token"},
		// The gateway resolves the whole path and routes membership prod.
		{"/v1/projects/p/locations/global/gkeMemberships/m/../prod/api/v1/namespaces/kube-system/secrets/token", "/../prod/api/v1/namespaces/kube-system/secrets/token"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("connectgateway.googleapis.com", tc.target))
			if got := s.Metadata[inspect.MetadataHTTPResourceUnresolved]; got != tc.unresolved {
				t.Errorf("unresolved = %q, want %q", got, tc.unresolved)
			}
			if !rules.Evaluate(s).Denied {
				t.Errorf("allowed; resource %q", s.HTTP.Resource)
			}
		})
	}
}
