package audit

import "testing"

// Clearing a sidecar's identity binding must not be logged as deleting the
// sidecar, and deleting the sidecar must still read as such.
func TestDeriveResourceTypeSidecarIdentity(t *testing.T) {
	for path, want := range map[string]ResourceType{
		"/api/sidecars/gke-eu-ws-1/identity": ResourceSidecarIdentity,
		"/api/sidecars/gke-eu-ws-1":          "sidecars",
	} {
		if got := deriveResourceType(path); got != want {
			t.Errorf("%s: want %q, got %q", path, want, got)
		}
	}
}
