package clientexec

import (
	"os"
	"path/filepath"
	"testing"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// `hoop start control-plane` and `standalone` reassign plugintypes.AuditPath
// after package init. The WAL must follow the reassigned value, not the one
// seen at init.
func TestNewWritesWALUnderReassignedAuditPath(t *testing.T) {
	orig := plugintypes.AuditPath
	t.Cleanup(func() { plugintypes.AuditPath = orig })

	plugintypes.AuditPath = t.TempDir()
	want := filepath.Join(plugintypes.AuditPath, "clientexec", "org-sid-wal")

	// No gRPC server listens, so New fails after wal.Open; the folder is the
	// evidence that the WAL was opened under the reassigned path.
	_, _ = New(&Options{OrgID: "org", SessionID: "sid", ConnectionName: "bash", BearerToken: "x"})

	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("WAL folder %s not created (err=%v); clientexec resolved the audit path at init", want, err)
	}
}
