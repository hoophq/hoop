package inspect_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// The SSH vocabulary reaches operator configuration: an operation name is
// what someone writes in a guardrail rule. These are aliases of libhoop's
// definitions, so the spellings are pinned once, here, and a rename on
// either side of the seam fails this test rather than silently loading a
// rule that matches nothing.
func TestSSHOperationSpellings(t *testing.T) {
	if inspect.SSH != "ssh" {
		t.Errorf("protocol = %q, want ssh", inspect.SSH)
	}
	want := map[inspect.Operation]string{
		inspect.OpExecLine:    "exec_line",
		inspect.OpEnvSet:      "env_set",
		inspect.OpSFTPRead:    "sftp_read",
		inspect.OpSFTPWrite:   "sftp_write",
		inspect.OpSFTPRemove:  "sftp_remove",
		inspect.OpSFTPRename:  "sftp_rename",
		inspect.OpSFTPMkdir:   "sftp_mkdir",
		inspect.OpSFTPRmdir:   "sftp_rmdir",
		inspect.OpSFTPList:    "sftp_list",
		inspect.OpSFTPStat:    "sftp_stat",
		inspect.OpSFTPSetstat: "sftp_setstat",
		inspect.OpSFTPSymlink: "sftp_symlink",
	}
	if len(want) != 12 {
		t.Fatalf("the table lists %d operations, want the twelve ADR-0015 names", len(want))
	}
	for got, spelling := range want {
		if string(got) != spelling {
			t.Errorf("operation = %q, want %q", got, spelling)
		}
	}
}

// There is no sftp_open, and that is a decision rather than an omission: a
// client's open surfaces as the first read or write against the path, so an
// sftp_open rule would load and never fire.
func TestNoSFTPOpenOperation(t *testing.T) {
	for _, op := range []inspect.Operation{
		inspect.OpExecLine, inspect.OpEnvSet,
		inspect.OpSFTPRead, inspect.OpSFTPWrite, inspect.OpSFTPRemove,
		inspect.OpSFTPRename, inspect.OpSFTPMkdir, inspect.OpSFTPRmdir,
		inspect.OpSFTPList, inspect.OpSFTPStat, inspect.OpSFTPSetstat,
		inspect.OpSFTPSymlink,
	} {
		if op == "sftp_open" {
			t.Fatal("an sftp_open operation exists; a rule naming it would never fire")
		}
	}
}
