package http_test

import (
	"slices"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func decodeRequest(t *testing.T, c *codechttp.Codec, raw string) inspect.Statement {
	t.Helper()
	stmts, n, err := c.Decode(inspect.FromClient, []byte(raw))
	if err != nil || len(stmts) != 1 || n != len(raw) {
		t.Fatalf("Decode(%q): %d statements, consumed %d of %d, %v", raw, len(stmts), n, len(raw), err)
	}
	return stmts[0]
}

func get(host, target string) string {
	return "GET " + target + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
}

// Through Connect Gateway the Kubernetes path sits under the cluster's
// project/location/membership prefix. Policy sees the Kubernetes resource;
// the audit trail keeps the request line as sent.
func TestConnectGatewayPrefixIsStripped(t *testing.T) {
	for _, tc := range []struct {
		name, host, target, resource, prefix string
	}{
		{
			name:     "global endpoint",
			host:     "connectgateway.googleapis.com",
			target:   "/v1/projects/my-proj/locations/global/gkeMemberships/prod/api/v1/namespaces/default/secrets/db?watch=1",
			resource: "/api/v1/namespaces/default/secrets/db",
			prefix:   "/v1/projects/my-proj/locations/global/gkeMemberships/prod",
		},
		{
			name:     "regional endpoint with port, mixed-case literals",
			host:     "US-Central1-connectgateway.googleapis.com:443",
			target:   "/v1beta1/Projects/123456789012/Locations/us-central1/Memberships/c1/api/v1/Pods",
			resource: "/api/v1/Pods",
			prefix:   "/v1beta1/Projects/*/Locations/us-central1/Memberships/c1",
		},
		{
			name:     "the membership itself",
			host:     "connectgateway.googleapis.com.",
			target:   "/v1alpha1/projects/p/locations/l/memberships/m",
			resource: "/",
			prefix:   "/v1alpha1/projects/p/locations/l/memberships/m",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := get(tc.host, tc.target)
			s := decodeRequest(t, codechttp.New(codechttp.Options{}), raw)
			d := s.HTTP
			if d.Resource != tc.resource {
				t.Errorf("Resource = %q, want %q", d.Resource, tc.resource)
			}
			if want := []string{strings.ToLower(tc.resource)}; !slices.Equal(s.Tables, want) {
				t.Errorf("Tables = %q, want %q", s.Tables, want)
			}
			if got := s.Metadata["http.resource_prefix"]; got != tc.prefix {
				t.Errorf("recorded prefix = %q, want %q", got, tc.prefix)
			}
			if d.Target != tc.target {
				t.Errorf("Target = %q, want the request-target as sent", d.Target)
			}
			if want := "GET " + tc.target; s.Text != want {
				t.Errorf("Text = %q, want %q", s.Text, want)
			}
			if path, _, _ := strings.Cut(tc.target, "?"); d.Path != path {
				t.Errorf("Path = %q, want %q", d.Path, path)
			}
		})
	}
}

// The reason the prefix goes: a Kubernetes rule must hold through the
// gateway, where the full resource would match nothing and let the request
// through.
func TestKubernetesRuleHoldsThroughConnectGateway(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-secrets", Type: policy.MatchHTTPResource}.
			WithResources("/api/v1/namespaces/*/secrets/*"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := decodeRequest(t, codechttp.New(codechttp.Options{}), get(
		"connectgateway.googleapis.com",
		"/v1/projects/p/locations/global/gkeMemberships/prod/api/v1/namespaces/kube-system/secrets/token"))
	if !rules.Evaluate(s).Denied {
		t.Fatalf("a secrets read through Connect Gateway was allowed; resource %q", s.HTTP.Resource)
	}
}

// Only the exact gateway shape is rewritten. Anything else keeps the
// resource libhoop derived, and carries no prefix record.
func TestConnectGatewayPrefixNeedsTheExactShape(t *testing.T) {
	const gw = "connectgateway.googleapis.com"
	const k8s = "/v1/projects/p/locations/l/gkeMemberships/m/api/v1/pods"
	for _, tc := range []struct{ name, host, target string }{
		{"another host", "api.example.com", k8s},
		{"a host that only ends in the gateway name", "evilconnectgateway.googleapis.com", k8s},
		{"the gateway name as a subdomain", gw + ".example.com", k8s},
		{"six segments", gw, "/v1/projects/p/locations/l/gkeMemberships"},
		{"unknown version", gw, "/v2/projects/p/locations/l/gkeMemberships/m/api/v1/pods"},
		{"projects misspelled", gw, "/v1/project/p/locations/l/gkeMemberships/m/api/v1/pods"},
		{"locations misspelled", gw, "/v1/projects/p/regions/l/gkeMemberships/m/api/v1/pods"},
		{"another collection", gw, "/v1/projects/p/locations/l/clusters/m/api/v1/pods"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := decodeRequest(t, codechttp.New(codechttp.Options{}), get(tc.host, tc.target))
			if s.HTTP.Resource != tc.target {
				t.Errorf("Resource = %q, want it untouched: %q", s.HTTP.Resource, tc.target)
			}
			if want := []string{strings.ToLower(tc.target)}; !slices.Equal(s.Tables, want) {
				t.Errorf("Tables = %q, want %q", s.Tables, want)
			}
			if p, ok := s.Metadata["http.resource_prefix"]; ok {
				t.Errorf("a prefix %q was recorded for a request that has none", p)
			}
		})
	}
}
