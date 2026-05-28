//go:build integration

package integration

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"

	"golang.org/x/crypto/ssh"
)

// waitForUpstreamReady gives libhoop's async SSH dial+handshake time
// to settle before the test issues real channel work. Without this,
// NewSession returns instantly (the bridge accepts session channels
// locally) but the upstream sshd may not yet be reachable through
// libhoop's pending ssh.Dial, and the subsequent exec/shell request
// races the dial.
const waitForUpstreamReady = 500 * time.Millisecond

// Base SSH protocol scenarios driven through the PipedSSHClient
// bridge. These exercise processSSHProtocol end-to-end: the test
// stands up a real x/crypto/ssh.Client, the bridge translates SSH
// channel/request events into sshtypes envelopes, the agent forwards
// them through libhoop's SSH proxy, and the real sshd container in
// testcontainers does the actual work.
//
// These tests give us first-time coverage of the SSH happy paths
// (auth, exec, pty, multi-channel, port-forward) that processSSHProtocol
// has carried for years without an integration test.

// passwordClientConfig builds an SSH client config that authenticates
// the test against the linuxserver/openssh-server container using the
// fixed test credentials.
func passwordClientConfig(user, pass string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
}

// TestSSH_PasswordAuth_Connect verifies the end-to-end SSH handshake
// with password authentication: PipedSSHClient → net.Pipe →
// sshtypes envelopes → MockTransport → controller.Agent →
// libhoop.SSHProxy → upstream sshd. The assertion is "the
// ssh.Client constructed successfully" — under the hood that
// required KEX, password auth, and the global-request loop to all
// complete cleanly.
func TestSSH_PasswordAuth_Connect(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-1",
		passwordClientConfig(ssh.User, ssh.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}
	if pc.Client == nil {
		t.Fatalf("expected non-nil SSH client after successful dial")
	}
}

// TestSSH_PubkeyAuth_Connect verifies the end-to-end SSH handshake
// with public-key authentication. The test generates an RSA keypair,
// installs the public key as the upstream sshd's authorized_keys for
// the test user, and configures the agent's libhoop with the private
// key via the AUTHORIZED_SERVER_KEYS env var. A successful Client.NewSession
// proves the key-based auth path works end-to-end.
func TestSSH_PubkeyAuth_Connect(t *testing.T) {
	key := testutil.GenerateSSHKey(t)
	sshC := testutil.StartSSHWithPublicKey(t, key.AuthorizedKey)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSessionWithKey(t, tr, sshC.Host, sshC.Port, sshC.User, key.PrivateKeyPEM)
	demux := testutil.StartRecvDemux(t, tr)

	// Use the same key for the local bridge handshake — the bridge
	// accepts anything, but this keeps the test plumbing honest.
	clientCfg := &ssh.ClientConfig{
		User:            sshC.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key.Signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-pubkey", clientCfg, nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	// NewSession will trigger libhoop's pubkey-based dial against
	// the upstream sshd. If the upstream rejects the key, the agent
	// emits SessionClose; if it accepts, NewSession completes.
	session, err := pc.Client.NewSession()
	if err != nil {
		t.Fatalf("new session over pubkey auth: %v", err)
	}
	defer session.Close()

	time.Sleep(waitForUpstreamReady)

	out, err := session.Output("echo pubkey-ok")
	if err != nil {
		t.Fatalf("exec over pubkey-authed session: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "pubkey-ok" {
		t.Errorf("expected 'pubkey-ok', got %q", got)
	}
}

// TestSSH_BadCredentials configures the agent with a bad upstream
// password and asserts the session closes cleanly when libhoop's
// dial fails authentication.
//
// The test's local SSH handshake (between PipedSSHClient and the
// in-process bridge) always succeeds because the bridge accepts any
// auth — the real upstream authentication happens inside libhoop's
// ssh.Dial when the first channel-open packet triggers proxy
// creation. So the assertion is "the agent surfaces auth failure as
// SessionClose", not "the SSH client handshake errors out."
func TestSSH_BadCredentials(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	// Open the session with a bad password baked into env vars. The
	// agent stores these in connection params and replays them to
	// libhoop's ssh.Dial.
	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, "definitely-not-the-password")
	demux := testutil.StartRecvDemux(t, tr)

	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-bad",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("local piped ssh handshake should not have failed: %v", err)
	}

	// Trigger a channel open so the agent invokes libhoop, which
	// dials the upstream with the bad credentials and fails auth.
	// NewSession returns when the bridge accepts the channel locally,
	// not when the upstream confirms — so we explicitly wait for the
	// SessionClose response that surfaces libhoop's auth failure.
	go func() {
		session, err := pc.Client.NewSession()
		if err == nil {
			_ = session.Close()
		}
	}()

	closePkt := waitForAgentSessionClose(t, demux, sid, 10*time.Second)
	body := string(closePkt.Payload)
	if !strings.Contains(strings.ToLower(body), "auth") &&
		!strings.Contains(strings.ToLower(body), "unable") {
		t.Errorf("expected SessionClose body to mention auth failure, got: %q", body)
	}
}

// waitForAgentSessionClose drains demux.SessionChannel() until a
// SessionClose for the given session ID arrives, or fails the test
// on timeout. Used by tests that expect the agent to emit a clean
// SessionClose response (e.g. on auth failure or upstream errors).
func waitForAgentSessionClose(t *testing.T, demux *testutil.RecvDemux, sid string, timeout time.Duration) *pb.Packet {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case pkt, ok := <-demux.SessionChannel():
			if !ok {
				t.Fatalf("session channel closed before SessionClose for sid=%s", sid)
			}
			if string(pkt.Spec[pb.SpecGatewaySessionID]) == sid &&
				pkt.Type == pbclient.SessionClose {
				return pkt
			}
		case <-deadline:
			t.Fatalf("timed out after %v waiting for SessionClose on sid=%s", timeout, sid)
			return nil
		}
	}
}

// TestSSH_ExecCommand runs `echo hello` over the established
// connection and asserts the stdout output matches. This exercises
// channel-open + exec request + data forwarding + exit-status path
// end-to-end.
func TestSSH_ExecCommand(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-exec",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	session, err := pc.Client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	time.Sleep(waitForUpstreamReady)

	out, err := session.Output("echo hello")
	if err != nil {
		t.Fatalf("exec echo hello: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

// TestSSH_ExitStatus_Forwarding runs a command with a non-zero exit
// (`false`) and asserts the SSH library surfaces the exit code. This
// proves the ServerSSHRequest with type "exit-status" is forwarded
// from the upstream sshd back through libhoop and the bridge to the
// test's ssh.Session.
func TestSSH_ExitStatus_Forwarding(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-exit",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	session, err := pc.Client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	err = session.Run("false")
	if err == nil {
		t.Fatalf("expected non-nil error for `false` command, got nil")
	}
	var exitErr *ssh.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("expected *ssh.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitStatus() != 1 {
		t.Errorf("expected exit status 1, got %d", exitErr.ExitStatus())
	}
}

// TestSSH_InteractiveShell_PTY opens an interactive shell with a
// pty-req, writes a command to stdin, reads output from stdout, and
// closes cleanly. Exercises pty allocation and bidirectional data
// flow on the same channel.
func TestSSH_InteractiveShell_PTY(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-pty",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	session, err := pc.Client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm", 24, 80, modes); err != nil {
		t.Fatalf("request pty: %v", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutBuf := &bytes.Buffer{}
	session.Stdout = stdoutBuf

	if err := session.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	// Write a command that produces a known marker, then exit. The
	// pty echoes input (with ECHO=0 above this is supposed to be off
	// but busybox sh still produces predictable output).
	if _, err := io.WriteString(stdin, "echo PIPED_OK; exit\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	// The shell exits when it sees `exit`; session.Wait returns the
	// exit code. We don't care about the code here, just that the
	// output contains our marker.
	_ = session.Wait()
	got := stdoutBuf.String()
	if !strings.Contains(got, "PIPED_OK") {
		t.Errorf("expected PTY shell output to contain PIPED_OK marker, got %q", got)
	}
}

// TestSSH_MultipleChannels opens two exec channels on the same SSH
// connection and runs commands on each. Both must complete cleanly.
// This proves the bridge correctly multiplexes channels by ChannelID
// (no cross-talk in the inbound packet pump).
func TestSSH_MultipleChannels(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-multi",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	runOnce := func(label, cmd, want string) {
		t.Helper()
		session, err := pc.Client.NewSession()
		if err != nil {
			t.Fatalf("[%s] new session: %v", label, err)
		}
		defer session.Close()
		out, err := session.Output(cmd)
		if err != nil {
			t.Fatalf("[%s] run %q: %v", label, cmd, err)
		}
		got := strings.TrimSpace(string(out))
		if got != want {
			t.Errorf("[%s] expected %q, got %q", label, want, got)
		}
	}

	// Two sequential channels (not concurrent — that's covered by
	// the concurrency tests). The point here is that the bridge
	// correctly handles a second channel after the first closes.
	runOnce("ch-1", "echo one", "one")
	runOnce("ch-2", "echo two", "two")
}

// TestSSH_SessionClose_CleanShutdown opens a session, runs a
// command, then injects a SessionClose packet and asserts the
// upstream connection drops cleanly. The session RW lock guarantees
// no orphan proxies remain.
func TestSSH_SessionClose_CleanShutdown(t *testing.T) {
	sshC := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-close",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	session, err := pc.Client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := session.Output("echo ready"); err != nil {
		t.Fatalf("warm-up exec: %v", err)
	}
	_ = session.Close()

	// Inject SessionClose for this connection and verify the
	// upstream connection count drops to zero within a reasonable
	// window.
	closePkt := testutil.BuildSessionClosePacket(pc.SessionID(), "0")
	tr.Inject(closePkt)
	sshC.WaitForEstablishedSSHConnections(t, 0, 10*time.Second)
}

// TestSSH_DirectTCPIP_PortForward opens a port-forward channel
// through the established SSH connection, dials nginx via the
// forward, sends an HTTP GET, and asserts the 200 response makes
// the round trip. Exercises the direct-tcpip channel type and
// bidirectional data flow on a non-session channel.
func TestSSH_DirectTCPIP_PortForward(t *testing.T) {
	// linuxserver/openssh-server defaults AllowTcpForwarding=no, so
	// we explicitly need the variant that flips it on after the
	// container starts.
	sshC := testutil.StartSSHWithForwarding(t)
	httpTarget := testutil.StartHTTPTarget(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sid := testutil.OpenSSHSession(t, tr, sshC.Host, sshC.Port, sshC.User, sshC.Password)
	demux := testutil.StartRecvDemux(t, tr)
	pc, err := testutil.DialPipedSSH(t, tr, demux, sid, "piped-fwd",
		passwordClientConfig(sshC.User, sshC.Password), nil)
	if err != nil {
		t.Fatalf("piped ssh dial: %v", err)
	}

	// Give libhoop time to finish the async upstream SSH handshake
	// before we ask it to open a forward channel. The forward dial
	// (and HTTP roundtrip) happen inside the upstream SSH container,
	// so they need the SSH channel to be fully established first.
	time.Sleep(waitForUpstreamReady)

	// Dial nginx through the SSH connection. ssh.Client.Dial opens a
	// direct-tcpip channel against the configured destination; the
	// agent forwards it through libhoop, which connects to nginx
	// from inside the upstream SSH container.
	target := httpTarget.ContainerIP + ":" + httpTarget.ContainerPort
	conn, err := pc.Client.Dial("tcp", target)
	if err != nil {
		t.Fatalf("ssh dial through forward to %s: %v", target, err)
	}
	defer conn.Close()

	if _, err := io.WriteString(conn, "GET / HTTP/1.0\r\nHost: nginx\r\n\r\n"); err != nil {
		t.Fatalf("http write through forward: %v", err)
	}

	// SSH channels (x/crypto/ssh.chanConn) don't honor
	// SetReadDeadline — SetReadDeadline returns "deadline not
	// supported" and io.Copy blocks until the channel closes.
	// Instead of waiting for the channel to close (libhoop holds
	// the close until upstream requests-done fires, which never
	// happens for direct-tcpip — that's a libhoop limitation
	// tracked in ENG-396's sibling cleanups), read with a soft
	// completion signal: once we see the HTTP status line and
	// 200 bytes of body, we know nginx answered.
	readDone := make(chan readResult, 1)
	go func() {
		buf := &bytes.Buffer{}
		tmp := make([]byte, 4096)
		for {
			n, err := conn.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				// Once we have the status line and a chunk of
				// body we've proven the forward works. The
				// SSH channel won't close cleanly until the
				// session ends, so don't wait for io.EOF.
				if bytes.Contains(buf.Bytes(), []byte("HTTP/")) &&
					buf.Len() >= 500 {
					break
				}
			}
			if err != nil {
				break
			}
		}
		readDone <- readResult{body: buf.String()}
	}()

	var resp string
	select {
	case r := <-readDone:
		resp = r.body
	case <-time.After(15 * time.Second):
		t.Fatalf("http read through forward timed out after 15s")
	}

	if !strings.HasPrefix(resp, "HTTP/1.0 200") && !strings.HasPrefix(resp, "HTTP/1.1 200") {
		t.Errorf("expected 200 OK from nginx via port-forward, got first line: %q",
			strings.SplitN(resp, "\n", 2)[0])
	}
}

type readResult struct {
	body string
	err  error
}

// asExitError unwraps an SSH command error into a *ssh.ExitError.
// The go.crypto library returns *ExitError for non-zero command
// exits; callers test on it to read the status code.
func asExitError(err error, target **ssh.ExitError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*ssh.ExitError); ok {
		*target = e
		return true
	}
	return false
}
