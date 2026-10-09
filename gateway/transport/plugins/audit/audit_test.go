package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// A session directory that cannot be created must not stop the boot. The
// session that needs it fails instead, and the error names the directory and
// the variable that fixes it.
func TestUnwritableAuditPathFailsTheSessionNotTheBoot(t *testing.T) {
	// A path under a regular file cannot be created, even by root.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(blocker, "sessions")

	prevPath, prevDelay := plugintypes.AuditPath, unwritableAuditPathDelay
	plugintypes.AuditPath, unwritableAuditPathDelay = auditPath, 0
	t.Cleanup(func() { plugintypes.AuditPath, unwritableAuditPathDelay = prevPath, prevDelay })

	p := New()
	if err := p.OnStartup(plugintypes.Context{}); err != nil {
		t.Fatalf("OnStartup stopped the boot: %v", err)
	}

	err := p.OnConnect(plugintypes.Context{
		OrgID:      "org",
		SID:        "sid",
		ParamsData: plugintypes.GenericMap{},
	})
	if err == nil {
		t.Fatal("OnConnect opened a session without a writable session directory")
	}
	for _, want := range []string{auditPath, "PLUGIN_AUDIT_PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
