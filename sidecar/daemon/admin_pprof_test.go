package daemon

import (
	"strings"
	"testing"
)

// The read routes have no authentication, so profiles must not reach the
// network: admin.pprof is refused unless admin.api_listen is loopback.
func TestAdminPprofRequiresLoopbackAPIListen(t *testing.T) {
	for _, tc := range []struct {
		apiListen string
		ok        bool
	}{
		{"127.0.0.1:19001", true},
		{"[::1]:19001", true},
		{"localhost:19001", true},
		{"0.0.0.0:19001", false},
		{"10.0.0.5:19001", false},
		{":19001", false},
		{"", false},
	} {
		problems := AdminConfig{Listen: "127.0.0.1:19000", APIListen: tc.apiListen, Pprof: true}.validate()
		if tc.ok != (len(problems) == 0) {
			t.Errorf("api_listen %q: problems %v", tc.apiListen, problems)
		}
		if !tc.ok && !strings.Contains(strings.Join(problems, ";"), "loopback") {
			t.Errorf("api_listen %q: refusal does not name loopback: %v", tc.apiListen, problems)
		}
	}

	if problems := (AdminConfig{Listen: "127.0.0.1:19000", APIListen: "0.0.0.0:19001"}).validate(); len(problems) != 0 {
		t.Fatalf("pprof off refused a public api listener: %v", problems)
	}
}
