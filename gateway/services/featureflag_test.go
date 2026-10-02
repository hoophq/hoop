package services

import (
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
)

// The flag changes what existing orgs see, so it is born off. An org that
// turns it on must not turn it on for its neighbours.
func TestSidecarListenersEnabled(t *testing.T) {
	const on, off = "org-listeners-on", "org-listeners-off"
	if SidecarListenersEnabled(off) {
		t.Fatal("want the flag off by default")
	}
	featureflag.Set(on, featureflag.FlagSidecarListeners, true)
	t.Cleanup(func() { featureflag.Set(on, featureflag.FlagSidecarListeners, false) })

	if !SidecarListenersEnabled(on) {
		t.Error("want the flag on for the org that enabled it")
	}
	if SidecarListenersEnabled(off) {
		t.Error("the flag leaked into another org")
	}
}
