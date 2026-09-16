package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// activities returns the KindActivity records of one name.
func activities(events []audit.Event, name string) []audit.Event {
	var out []audit.Event
	for _, ev := range events {
		if ev.Kind == audit.KindActivity && ev.Metadata[audit.MetadataActivity] == name {
			out = append(out, ev)
		}
	}
	return out
}

// A shell produces no statements, so its whole audit record is its open, its
// geometry, its duration and its byte counts. A session-end row showing zero
// statements and nothing else would read as a connection that did nothing.
func TestSSHShellLeavesCountsAndNoContent(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	ctx := context.Background()

	if err := c.gate.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// What libhoop reports for an interactive session: the geometry at
	// close, then the connection's totals.
	c.event(ctx, "session_close", map[string]string{
		"term": "xterm-256color", "cols": "120", "rows": "40",
		"bytes_in": "37", "bytes_out": "2048", "duration_ms": "240000",
	})
	exit := 0
	if err := c.close(ctx, codecssh.Stats{
		BytesIn: 37, BytesOut: 2048, ExitCode: &exit,
	}); err != nil {
		t.Fatal(err)
	}

	events := sink.snapshot()
	var start, end int
	for _, ev := range events {
		switch ev.Kind {
		case audit.KindSessionStart:
			start++
		case audit.KindSessionEnd:
			end++
		case audit.KindStatement, audit.KindViolation:
			t.Errorf("a shell produced a %s event; v1 reconstructs no keystrokes", ev.Kind)
		}
		if ev.Statement != "" {
			t.Errorf("an event carries statement text %q", ev.Statement)
		}
	}
	if start != 1 || end != 1 {
		t.Errorf("start=%d end=%d, want one of each", start, end)
	}

	geometry := activities(events, "session_close")
	if len(geometry) != 1 {
		t.Fatalf("recorded %d session_close events", len(geometry))
	}
	if geometry[0].Metadata["cols"] != "120" || geometry[0].Metadata["rows"] != "40" {
		t.Errorf("the geometry did not reach the trail: %v", geometry[0].Metadata)
	}

	totals := activities(events, "connection_close")
	if len(totals) != 1 {
		t.Fatalf("recorded %d connection_close events", len(totals))
	}
	if totals[0].Metadata["bytes_out"] != "2048" || totals[0].Metadata["exit_code"] != "0" {
		t.Errorf("the totals did not reach the trail: %v", totals[0].Metadata)
	}
}

// A pty is a property of a session, not a session of its own. Two rows for
// one thing that happened is what this asserts against.
func TestSSHPTYLeavesNoRecordOfItsOwn(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	if r := c.pty(context.Background(), "xterm", 80, 24); r != nil {
		t.Fatalf("an admitted pty was refused: %v", r)
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("a pty request wrote %d events; its geometry rides on the session's record", len(got))
	}
}

// A capability the listener does not admit is refused by libhoop and
// reported once. Recording it twice would double-count the denials a
// security team reads.
func TestSSHRefusedCapabilityLeavesOneEvent(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	c.event(context.Background(), "capability_refused", map[string]string{
		"capability": "sftp",
		"reason":     "this listener does not admit file transfer",
	})
	got := activities(sink.snapshot(), "capability_refused")
	if len(got) != 1 {
		t.Fatalf("recorded %d events for one refusal", len(got))
	}
	if got[0].Metadata["capability"] != "sftp" {
		t.Errorf("the record does not name the capability: %v", got[0].Metadata)
	}
}

// An sftp transfer records its operation, path, direction and byte count
// unconditionally. The file's bytes are never recorded and no config key
// offers them: a setting that records nothing is the silent failure this
// codebase refuses.
func TestSSHSFTPTransferRecordsPathAndByteCount(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	ctx := context.Background()

	if r := c.sftpOp(ctx, inspect.OpSFTPRead, "/srv/reports/q3.csv", ""); r != nil {
		t.Fatal(r)
	}
	c.event(ctx, "sftp_transfer", map[string]string{
		"capability": "sftp", "operation": "sftp_read",
		"path": "/srv/reports/q3.csv", "direction": "download", "bytes": "84213",
	})

	events := sink.snapshot()
	transfers := activities(events, "sftp_transfer")
	if len(transfers) != 1 {
		t.Fatalf("recorded %d transfers", len(transfers))
	}
	m := transfers[0].Metadata
	if m["path"] != "/srv/reports/q3.csv" || m["bytes"] != "84213" || m["direction"] != "download" {
		t.Errorf("the transfer record is incomplete: %v", m)
	}

	// The operation itself is a statement, with its verdict. The bytes are
	// not anywhere.
	var statements int
	for _, ev := range events {
		if ev.Kind == audit.KindStatement {
			statements++
			if ev.Operation != inspect.OpSFTPRead || ev.Statement != "/srv/reports/q3.csv" {
				t.Errorf("statement = %q as %q", ev.Statement, ev.Operation)
			}
		}
	}
	if statements != 1 {
		t.Errorf("recorded %d statements for one file operation", statements)
	}
}

// An exec is audited with the command IN FULL, and its output is not
// retained anywhere.
func TestSSHExecRecordsTheCommandAndNotItsOutput(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	ctx := context.Background()

	const cmd = "psql -c 'select * from customers' -h db.internal"
	if r := c.exec(ctx, cmd); r != nil {
		t.Fatal(r)
	}
	if err := c.close(ctx, codecssh.Stats{BytesIn: 12, BytesOut: 90210}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindStatement && ev.Statement == cmd {
			found = true
		}
		// The only place a byte count belongs is the totals; no record may
		// carry what the command printed.
		if strings.Contains(ev.Statement, "customers\n") {
			t.Error("command output reached the trail")
		}
	}
	if !found {
		t.Error("the command was not recorded in full")
	}
}
