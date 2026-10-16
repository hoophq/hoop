package clientexec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/tidwall/wal"
)

// Output past maxResponseBytes must not be stored, and the response must stay
// the same as when it was: the first maxResponseBytes, flagged as truncated.
func TestWriteStopsPastResponseCap(t *testing.T) {
	orig := maxResponseBytes
	t.Cleanup(func() { maxResponseBytes = orig })
	maxResponseBytes = 90

	for _, tt := range []struct {
		name       string
		chunks     int // of 30 bytes each
		wantStored int
		wantTrunc  bool
	}{
		{"under the cap", 2, 2, false},
		{"exactly the cap", 3, 3, false},
		{"one chunk past the cap", 4, 4, true},
		{"far past the cap", 50, 4, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wlog, err := wal.Open(t.TempDir(), wal.DefaultOptions)
			if err != nil {
				t.Fatal(err)
			}
			defer wlog.Close()
			c := &clientExec{wlog: wlog}

			var all []byte
			for i := range tt.chunks {
				chunk := bytes.Repeat(fmt.Appendf(nil, "%03d", i), 10)
				all = append(all, chunk...)
				if err := c.write(chunk); err != nil {
					t.Fatalf("write chunk %d: %v", i, err)
				}
			}

			if stored, _ := wlog.LastIndex(); stored != uint64(tt.wantStored) {
				t.Errorf("stored %d chunks, want %d", stored, tt.wantStored)
			}
			out, trunc, err := c.readAll()
			if err != nil {
				t.Fatal(err)
			}
			want := string(all[:min(len(all), maxResponseBytes)])
			if out != want || trunc != tt.wantTrunc {
				t.Errorf("readAll=(%q, %v), want (%q, %v)", out, trunc, want, tt.wantTrunc)
			}
		})
	}
}

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
