package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// sshTestMasker rewrites one literal into a replacement, so a test can
// choose whether the rewrite preserves the length.
type sshTestMasker struct {
	find    []byte
	replace []byte
}

func (m sshTestMasker) Mask(data []byte) ([]byte, []string, int) {
	n := bytes.Count(data, m.find)
	if n == 0 {
		return data, nil, 0
	}
	return bytes.ReplaceAll(data, m.find, m.replace), []string{"EMAIL_ADDRESS"}, n
}

func (m sshTestMasker) MaskCell(_ string, value []byte) ([]byte, []string, int) {
	return m.Mask(value)
}

func sshTestMaskConn(t *testing.T, masker gate.Masker) (*sshConnState, *sshTestSink) {
	t.Helper()
	sess := session.New(inspect.SSH, session.Identity{Subject: "alice"})
	sess.Connection = "prod-endpoint"
	sink := &sshTestSink{}
	g, err := gate.NewStatementGate(sess, gate.Config{
		Protocol: inspect.SSH,
		Audit:    sink,
		Masker:   masker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &sshConnState{
		gate:  g,
		stmts: sshStatements{},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, sink
}

// Masking is a rewrite in flight. The masked bytes go out, neither version is
// kept, and the trail records only WHAT was rewritten.
func TestSSHMasksTerminalOutput(t *testing.T) {
	c, sink := sshTestMaskConn(t, sshTestMasker{
		find: []byte("alice@example.com"), replace: []byte("*****************")})

	out, r := c.streamData(context.Background(), inspect.FromServer,
		[]byte("user: alice@example.com\r\n"))
	if r != nil {
		t.Fatalf("a length-preserving rewrite was refused: %v", r)
	}
	if bytes.Contains(out, []byte("alice@example.com")) {
		t.Errorf("the address survived masking: %q", out)
	}
	if len(out) != len("user: alice@example.com\r\n") {
		t.Errorf("the stream changed length: %d", len(out))
	}

	var masked int
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindMasked {
			masked++
			if strings.Contains(strings.Join(ev.MaskedEntities, ","), "alice@example.com") {
				t.Error("the audit record carries the masked value in the clear")
			}
		}
		if strings.Contains(ev.Statement, "alice@example.com") {
			t.Error("terminal output reached the trail as a statement")
		}
	}
	if masked != 1 {
		t.Errorf("recorded %d masked events, want one", masked)
	}
}

// A rule protects what the session SEES. Rewriting what the user typed would
// change the command that runs, which is a denial wearing a redaction's
// clothes; the input side has guardrails, which refuse instead.
func TestSSHDoesNotRewriteClientInput(t *testing.T) {
	c, _ := sshTestMaskConn(t, sshTestMasker{
		find: []byte("alice@example.com"), replace: []byte("*****************")})

	in := []byte("mail alice@example.com")
	out, r := c.streamData(context.Background(), inspect.FromClient, in)
	if r != nil {
		t.Fatal(r)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("client input was rewritten: %q", out)
	}
}

// The load check refuses every variable-length strategy, so a mismatch here
// means the masker did something its configuration said it would not. The
// bytes would still be forwarded, shifted, and the client would read a
// corrupted stream rather than an error — so the stream fails CLOSED.
func TestSSHLengthChangingMaskFailsTheStreamClosed(t *testing.T) {
	c, sink := sshTestMaskConn(t, sshTestMasker{
		find: []byte("alice@example.com"), replace: []byte("[REDACTED:EMAIL]")})

	out, r := c.streamData(context.Background(), inspect.FromServer,
		[]byte("user: alice@example.com\r\n"))
	if r == nil {
		t.Fatal("a length-changing rewrite was forwarded; the terminal would desynchronize")
	}
	if out != nil {
		t.Errorf("bytes were returned alongside the refusal: %q", out)
	}
	if !strings.Contains(r.String(), "without changing its length") {
		t.Errorf("refusal = %q, want it to say why", r.String())
	}

	var failures int
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindActivity && ev.Metadata[audit.MetadataActivity] == "mask_failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Errorf("recorded %d mask failures, want one; a closed stream must not be silent", failures)
	}
}

// A download is masked the same way terminal output is: one rule set covers
// every content-bearing stream on the lane.
func TestSSHMasksFileTransfer(t *testing.T) {
	c, _ := sshTestMaskConn(t, sshTestMasker{
		find: []byte("alice@example.com"), replace: []byte("*****************")})

	out, r := c.sftpData(context.Background(), "/srv/users.csv", []byte("id,alice@example.com\n"))
	if r != nil {
		t.Fatal(r)
	}
	if bytes.Contains(out, []byte("alice@example.com")) {
		t.Errorf("the address survived a download: %q", out)
	}
}

// Nil is not an identity function across the seam: the file-transfer
// subsystem streams a transfer when there is no hook and buffers it whole
// when there is one, so a lane with no rules must wire neither.
func TestSSHUnmaskedLaneWiresNoRewriteHooks(t *testing.T) {
	c, _ := sshTestMaskConn(t, nil)
	h := c.callbacks()
	if h.StreamData != nil || h.SFTPData != nil {
		t.Error("a lane with no masker wired a rewrite hook; every transfer would buffer whole")
	}

	c, _ = sshTestMaskConn(t, sshTestMasker{find: []byte("x"), replace: []byte("y")})
	h = c.callbacks()
	if h.StreamData == nil || h.SFTPData == nil {
		t.Error("a masking lane wired no rewrite hook")
	}
}
