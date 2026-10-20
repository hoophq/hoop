package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Only the fixed vocabulary leaves the process: a value an operator could
// fill with a customer name reports "other", whatever it says.
func TestInstallSourceIsFixedVocabulary(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"", ""},
		{"   ", ""},
		{"managed-agents-http", InstallSourceManagedAgentsHTTP},
		{" Managed-Agents-HTTP\n", InstallSourceManagedAgentsHTTP},
		{"acme-prod", InstallSourceOther},
		{"other", InstallSourceOther},
	} {
		t.Setenv(InstallSourceEnvVar, tc.env)
		if got := InstallSource(); got != tc.want {
			t.Errorf("InstallSource() with %q = %q, want %q", tc.env, got, tc.want)
		}
	}
}

// Every event carries install-source when the template set it, and none
// when it did not: an empty property would count as a value in a breakdown.
func TestCommonPropertiesCarryInstallSourceOnlyWhenSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	t.Setenv(InstallSourceEnvVar, "")
	c := New(Options{WriteKey: "wk", Endpoint: srv.URL})
	defer c.Close()
	if v, ok := c.common["install-source"]; ok {
		t.Fatalf("install-source = %q although the env is unset", v)
	}

	t.Setenv(InstallSourceEnvVar, InstallSourceManagedAgentsHTTP)
	c = New(Options{WriteKey: "wk", Endpoint: srv.URL})
	defer c.Close()
	if got := c.common["install-source"]; got != InstallSourceManagedAgentsHTTP {
		t.Fatalf("install-source = %v, want %q", got, InstallSourceManagedAgentsHTTP)
	}
}
