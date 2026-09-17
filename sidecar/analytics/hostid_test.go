package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeIsOneOfTheFixedVocabulary(t *testing.T) {
	known := map[string]bool{
		RuntimeKubernetes: true, RuntimeDocker: true, RuntimeMacOS: true,
		RuntimeWindows: true, RuntimeLinux: true,
	}
	if got := Runtime(); !known[got] {
		t.Fatalf("Runtime() = %q, not in the vocabulary", got)
	}
	// A pod is also a container and also Linux; the pod wins, because it
	// is the fact that says the hostname and machine-id are not a machine's.
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if got := Runtime(); got != RuntimeKubernetes {
		t.Fatalf("Runtime() in a pod = %q, want %q", got, RuntimeKubernetes)
	}
}

// The operator's host id outranks anything derived, is hashed rather than
// sent as written, and is stable.
func TestHostIDEnvOverridesAndIsHashed(t *testing.T) {
	derived := HostID()
	t.Setenv(HostIDEnvVar, "node-a")
	id := HostID()
	if id == "" || id == "node-a" || len(id) != 64 || id != HostID() {
		t.Fatalf("env host id = %q", id)
	}
	if id == derived {
		t.Fatal("the operator's id must differ from the derived one")
	}
	// Two sidecars given the same node name are on the same host, whatever
	// their own hostnames say.
	if HostID() != id {
		t.Fatal("host id not stable under the same env")
	}
}

// Every event carries runtime, and host-id whenever the host can name
// itself; a sidecar id never equals a host id.
func TestCommonPropertiesCarryRuntimeAndHostID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := New(Options{WriteKey: "wk", Endpoint: srv.URL})
	defer c.Close()
	if c.common["runtime"] == nil || c.common["runtime"] == "" {
		t.Fatal("runtime missing from common properties")
	}
	hid, ok := c.common["host-id"]
	if HostID() != "" && !ok {
		t.Fatal("host-id missing although the host reports one")
	}
	if ok && hid == c.common["sidecar-id"] {
		t.Fatal("host-id and sidecar-id must never collide")
	}
}
