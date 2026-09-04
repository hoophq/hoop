package clientexec

import (
	"os"
	"path/filepath"
	"testing"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

func TestOpenClientExecWALDoesNotRequireAuditPath(t *testing.T) {
	blockedAuditPath := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(blockedAuditPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalAuditPath := plugintypes.AuditPath
	plugintypes.AuditPath = blockedAuditPath
	t.Cleanup(func() { plugintypes.AuditPath = originalAuditPath })

	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)

	wlog, folderName, err := openClientExecWAL()
	if err != nil {
		t.Fatalf("openClientExecWAL() failed with an unusable audit path: %v", err)
	}
	t.Cleanup(func() {
		_ = wlog.Close()
		_ = os.RemoveAll(folderName)
	})

	if got, want := filepath.Dir(folderName), os.TempDir(); got != want {
		t.Fatalf("WAL parent = %q, want temporary directory %q", got, want)
	}
	if err := wlog.Write(1, []byte("schema")); err != nil {
		t.Fatalf("write client execution WAL: %v", err)
	}
}
