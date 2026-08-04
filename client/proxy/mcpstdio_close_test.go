//go:build !windows

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

// startTestChild spawns sh running script, wired the way startMCPChild wires a
// real MCP server: its own process group, stdin a pipe.
//
// A real process is the point. The bug was that close() blocked forever on
// cmd.Wait() for a child that ignores SIGTERM, and no fake reproduces the
// interaction between signal disposition, process groups and Wait.
func startTestChild(t *testing.T, script string) *mcpChild {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	setMCPChildProcGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	c := &mcpChild{cmd: cmd, stdin: stdin, stdout: stdout}
	t.Cleanup(func() {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
	})
	return c
}

// alive reports whether pid is still a live process (not yet reaped).
func alive(pid int) bool {
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// closeWithin runs close() and fails if it does not return inside d.
func closeWithin(t *testing.T, c *mcpChild, d time.Duration, msg string) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.close()
	}()
	select {
	case <-done:
		return time.Since(start)
	case <-time.After(d):
		t.Fatal(msg)
		return 0
	}
}

// The reported bug: a child that ignores SIGTERM must still be killed, and
// close() must return.
//
// `trap ” TERM` makes the shell ignore SIGTERM outright, which is what a
// server with a broken handler looks like from out here. Before the escalation
// this blocked on cmd.Wait() forever, hanging whichever goroutine was tidying
// up — including the CLI's own shutdown path.
func TestCloseKillsAChildThatIgnoresSIGTERM(t *testing.T) {
	c := startTestChild(t, `trap '' TERM; while :; do sleep 0.1; done`)
	pid := c.cmd.Process.Pid

	// stdin EOF grace + SIGTERM grace + reap, plus slack for a loaded runner.
	budget := mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace + 5*time.Second
	elapsed := closeWithin(t, c, budget,
		"close blocked on a child that ignores SIGTERM; CLI shutdown would hang forever")

	// It must have actually escalated rather than returned early.
	if elapsed < mcpStdioTermGrace {
		t.Fatalf("close returned after %v, before the stdin grace elapsed", elapsed)
	}
	// And the process must be gone, not merely abandoned.
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Fatalf("pid %d survived close; the user's machine keeps running an MCP server", pid)
	}
}

// A well-behaved server exits on stdin EOF, and that must stay the fast path:
// no signals, and close() returns promptly rather than sitting out the grace.
func TestCloseReturnsPromptlyOnStdinEOF(t *testing.T) {
	// `cat` exits when its stdin closes.
	c := startTestChild(t, `cat`)
	pid := c.cmd.Process.Pid

	elapsed := closeWithin(t, c, mcpStdioTermGrace+5*time.Second,
		"close hung on a child that exits on stdin EOF")
	if elapsed >= mcpStdioTermGrace {
		t.Errorf("close took %v, want a prompt return: a polite child should never wait out the grace", elapsed)
	}
	if alive(pid) {
		t.Errorf("pid %d still running after close", pid)
	}
}

// A child that handles SIGTERM must be reaped at that rung, without waiting
// for the SIGKILL escalation.
func TestCloseStopsAtSIGTERMWhenHandled(t *testing.T) {
	// Ignores stdin EOF (reads from a fifo that never closes), but exits on
	// SIGTERM.
	c := startTestChild(t, `trap 'exit 0' TERM; while :; do sleep 0.1; done`)
	pid := c.cmd.Process.Pid

	budget := mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace + 5*time.Second
	elapsed := closeWithin(t, c, budget, "close hung on a child that handles SIGTERM")

	// It must not have needed the SIGKILL rung.
	if elapsed >= mcpStdioTermGrace+mcpStdioKillGrace {
		t.Errorf("close took %v, want it to finish at the SIGTERM rung", elapsed)
	}
	if alive(pid) {
		t.Errorf("pid %d still running after close", pid)
	}
}

// The signal must reach the whole process group. An npx-style server is a
// shell that execs node: signalling only the direct child leaves the real
// server running with the user's credentials.
func TestCloseReapsGrandchildren(t *testing.T) {
	// The shell ignores TERM and backgrounds a grandchild that also ignores
	// it; only a group-wide SIGKILL ends both. The grandchild's pid is
	// printed so the test can watch it.
	c := startTestChild(t, `trap '' TERM
( trap '' TERM; while :; do sleep 0.1; done ) &
echo $!
wait`)

	buf := make([]byte, 32)
	n, err := c.stdout.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("did not read the grandchild pid: %v", err)
	}
	var grandchild int
	if _, err := fmt.Sscan(string(buf[:n]), &grandchild); err != nil || grandchild <= 0 {
		t.Fatalf("unparseable grandchild pid %q: %v", buf[:n], err)
	}
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	budget := mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace + 5*time.Second
	closeWithin(t, c, budget, "close hung on a process tree that ignores SIGTERM")

	deadline := time.Now().Add(5 * time.Second)
	for alive(grandchild) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(grandchild) {
		t.Fatalf("grandchild %d survived; an orphaned MCP server keeps the user's credentials", grandchild)
	}
}

// close is idempotent and safe from several goroutines: dropChild and Close
// can both reach the same child when a session ends while the agent is
// reaping backends.
func TestCloseIsIdempotentUnderConcurrency(t *testing.T) {
	c := startTestChild(t, `cat`)

	done := make(chan struct{})
	for range 4 {
		go func() {
			c.close()
			done <- struct{}{}
		}()
	}
	for range 4 {
		select {
		case <-done:
		case <-time.After(mcpStdioTermGrace + 10*time.Second):
			t.Fatal("a concurrent close never returned")
		}
	}
	close(done)
}

// The last rung must be bounded too.
//
// SIGKILL cannot be caught, so a normal child always dies and cmd.Wait()
// always returns. A process wedged in uninterruptible sleep (D state: a stuck
// NFS read, a hung FUSE mount) does not — it stays unreaped until the I/O
// completes, which may be never. No signal reproduces that, so terminate is
// driven directly with an exit that never arrives.
//
// Abandoning the child leaks a zombie until the CLI exits. That is strictly
// better than a shutdown path that never returns.
func TestTerminateGivesUpOnAnUnreapableChild(t *testing.T) {
	c := startTestChild(t, `cat`)

	// Never closed: cmd.Wait() has, as far as terminate can tell, not returned.
	never := make(chan struct{})

	returned := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		c.terminate(never)
		returned <- time.Since(start)
	}()

	budget := mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace + 5*time.Second
	select {
	case elapsed := <-returned:
		// It must have walked every rung, not bailed out early.
		floor := mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace
		if elapsed < floor {
			t.Fatalf("terminate returned after %v, before the full ladder (%v) elapsed", elapsed, floor)
		}
	case <-time.After(budget):
		t.Fatal("terminate never returned for a child that cannot be reaped; CLI shutdown would hang forever")
	}
}

// ---- dispatch ---------------------------------------------------------------

// recordingTransport captures what MCPStdio sends back to the agent. Recv
// blocks forever: nothing in these tests drives the client stream inbound.
type recordingTransport struct {
	mu   sync.Mutex
	sent []*pb.Packet
}

func (t *recordingTransport) Send(pkt *pb.Packet) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, pkt)
	return nil
}

func (t *recordingTransport) Recv() (*pb.Packet, error) { select {} }
func (t *recordingTransport) StreamContext() context.Context {
	return context.Background()
}
func (t *recordingTransport) StartKeepAlive()       {}
func (t *recordingTransport) Close() (error, error) { return nil, nil }

func (t *recordingTransport) packets() []*pb.Packet {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*pb.Packet(nil), t.sent...)
}

// pidRecorder builds a command that records the pid of every child spawned
// from it, then behaves like `cat`: echoing whatever is written to its stdin,
// which is what makes the request stream observable from the reply stream.
//
// Counting spawns is the point of the file. The bug being guarded against is a
// SECOND MCP server appearing on the user's machine after its reaping packet
// has been spent, and only the pid trail shows that.
func pidRecorder(t *testing.T) (command []string, pids func() []int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pids")
	command = []string{"/bin/sh", "-c", `echo $$ >> "` + path + `"; exec cat`}
	return command, func() []int {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var out []int
		for _, line := range strings.Fields(string(raw)) {
			if pid, err := strconv.Atoi(line); err == nil {
				out = append(out, pid)
			}
		}
		return out
	}
}

func mcpRequest(t *testing.T, backendID, requestID string, command []string, payload []byte) *pb.Packet {
	t.Helper()
	encoded, err := pb.GobEncode(command)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	return &pb.Packet{
		Spec: map[string][]byte{
			pb.SpecMCPStdioBackendKey: []byte(backendID),
			pb.SpecMCPStdioRequestKey: []byte(requestID),
			pb.SpecMCPStdioCommandKey: encoded,
		},
		Payload: payload,
	}
}

func mcpClose(backendID string) *pb.Packet {
	return &pb.Packet{Spec: map[string][]byte{pb.SpecMCPStdioBackendKey: []byte(backendID)}}
}

func (m *MCPStdio) childCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.children)
}

// A request that arrives after its backend's close must not spawn anything.
//
// The agent orders the two with sendMu, so a request seen after the close is a
// straggler the gateway or the transport delayed. Spawning for it strands an
// MCP server whose reaping packet has already been spent: it keeps running
// with the user's credentials until the whole hoop session ends.
func TestRequestAfterCloseNeverSpawns(t *testing.T) {
	tr := &recordingTransport{}
	m := NewMCPStdio(tr, "sid")
	command, pids := pidRecorder(t)

	m.PacketCloseClient(mcpClose("b1"))
	m.PacketWriteClient(mcpRequest(t, "b1", "r1", command, []byte(`{"id":1}`)))

	// Long enough for a spawn to have happened and been recorded.
	time.Sleep(500 * time.Millisecond)

	if got := pids(); len(got) != 0 {
		t.Fatalf("spawned %v after the backend was reaped; an MCP server survives the session", got)
	}
	if n := m.childCount(); n != 0 {
		t.Fatalf("child map holds %d entries after a reaped backend's request", n)
	}
	// The agent is blocked on the ack, so the refusal must come back as an
	// error reply rather than silence.
	var replied bool
	for _, pkt := range tr.packets() {
		if len(pkt.Spec[pb.SpecMCPStdioErrorKey]) > 0 &&
			string(pkt.Spec[pb.SpecMCPStdioRequestKey]) == "r1" {
			replied = true
		}
	}
	if !replied {
		t.Error("no error reply for the straggling request; the agent would wait out its full timeout")
	}
}

// Requests interleaved with a close must leave nothing running.
//
// This is the shape the CLI's receive loop produces: a burst of requests for
// one backend and then its close, all handed over in wire order. Dispatching
// each on its own goroutine — what the loop used to do — lets a request run
// after the close and respawn the server it was meant to end.
func TestCloseReapsEverythingAmidstRequests(t *testing.T) {
	tr := &recordingTransport{}
	m := NewMCPStdio(tr, "sid")
	command, pids := pidRecorder(t)

	for i := range 20 {
		m.PacketWriteClient(mcpRequest(t, "b1", strconv.Itoa(i), command, []byte(`{"id":1}`)))
	}
	m.PacketCloseClient(mcpClose("b1"))
	for i := 20; i < 40; i++ {
		m.PacketWriteClient(mcpRequest(t, "b1", strconv.Itoa(i), command, []byte(`{"id":1}`)))
	}

	deadline := time.Now().Add(10 * time.Second)
	for m.childCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := m.childCount(); n != 0 {
		t.Fatalf("child map still holds %d entries after the close", n)
	}

	spawned := pids()
	if len(spawned) > 1 {
		t.Errorf("spawned %d children for one backend: %v, want at most one", len(spawned), spawned)
	}
	for _, pid := range spawned {
		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if alive(pid) {
			t.Errorf("pid %d survived the close; the MCP server keeps the user's credentials", pid)
		}
	}
}

// One backend's envelopes must reach its child in the order the agent sent
// them. An MCP session is a stateful conversation, and `initialize` overtaking
// the call that follows it is not something a server recovers from.
//
// `cat` echoes stdin to stdout, so the reply stream is the request stream: the
// order the child saw is exactly what comes back.
func TestRequestsReachTheChildInOrder(t *testing.T) {
	tr := &recordingTransport{}
	m := NewMCPStdio(tr, "sid")
	command, _ := pidRecorder(t)
	t.Cleanup(func() { _ = m.Close() })

	const total = 50
	for i := range total {
		payload := fmt.Appendf(nil, `{"seq":%d}`, i)
		m.PacketWriteClient(mcpRequest(t, "b1", strconv.Itoa(i), command, payload))
	}

	// Echoed lines come back as replies carrying a payload; the acks do not.
	var seqs []int
	deadline := time.Now().Add(10 * time.Second)
	for len(seqs) < total && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		seqs = seqs[:0]
		for _, pkt := range tr.packets() {
			if len(pkt.Payload) == 0 {
				continue
			}
			var msg struct {
				Seq *int `json:"seq"`
			}
			if err := json.Unmarshal(pkt.Payload, &msg); err != nil || msg.Seq == nil {
				continue
			}
			seqs = append(seqs, *msg.Seq)
		}
	}
	if len(seqs) != total {
		t.Fatalf("got %d echoed envelopes, want %d", len(seqs), total)
	}
	for i, seq := range seqs {
		if seq != i {
			t.Fatalf("envelope %d arrived at position %d: the child saw %v, out of order", seq, i, seqs)
		}
	}
}

// A stale pump's drop must not reap a healthy respawned child.
//
// pumpStdout drops the backend when its child's stdout closes, but by then the
// map may hold a child spawned for a later request. Deleting by backend id
// alone kills that live MCP server and the user's MCP session dies
// mid-conversation.
func TestDropChildIfIgnoresAReplacedChild(t *testing.T) {
	m := NewMCPStdio(&recordingTransport{}, "sid")
	stale := startTestChild(t, `cat`)
	fresh := startTestChild(t, `cat`)
	freshPID := fresh.cmd.Process.Pid

	m.children["b1"] = fresh
	m.dropChildIf("b1", stale)

	if m.children["b1"] != fresh {
		t.Fatal("a stale pump's drop removed the respawned child from the map")
	}
	if !alive(freshPID) {
		t.Fatalf("a stale pump's drop killed the live child pid %d", freshPID)
	}

	// The owning pump must still be able to drop it.
	m.dropChildIf("b1", fresh)
	if _, ok := m.children["b1"]; ok {
		t.Fatal("the child's own drop left it mapped")
	}
}

// The reap must not wait its turn behind the backend's queued requests, and it
// must still end the child.
//
// A child that has stopped reading its stdin wedges the worker in the pipe
// write once the buffer fills. Queueing the reap behind that would leave the
// MCP server running for the rest of the session — exactly what the close
// packet exists to prevent. It also wedges close() itself if the stdin close
// waits on the in-flight write, which is how Ctrl-C stopped returning.
func TestCloseIsNotBlockedByAWedgedChild(t *testing.T) {
	tr := &recordingTransport{}
	m := NewMCPStdio(tr, "sid")
	pidFile := filepath.Join(t.TempDir(), "pid")
	// Never reads stdin, so the pipe buffer fills and the worker's write
	// blocks; ignores SIGTERM so only the SIGKILL rung ends it.
	command := []string{"/bin/sh", "-c",
		`echo $$ > "` + pidFile + `"; trap '' TERM; while :; do sleep 0.1; done`}

	// Enough payload to overflow the pipe buffer (64 KiB on Linux, 8 KiB on
	// macOS) so the worker really is stuck in the write.
	payload := append([]byte(`{"pad":"`), append(make([]byte, 256<<10), '"', '}')...)
	for i := range 4 {
		m.PacketWriteClient(mcpRequest(t, "b1", strconv.Itoa(i), command, payload))
	}

	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
	}
	if pid == 0 {
		t.Fatal("the child never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	// Let the worker fill the pipe and block in the write.
	time.Sleep(500 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.PacketCloseClient(mcpClose("b1"))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the close blocked behind a wedged child; the receive loop would stall")
	}

	deadline = time.Now().Add(mcpStdioTermGrace + mcpStdioKillGrace + mcpStdioReapGrace + 10*time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Fatalf("pid %d outlived its close; the wedged MCP server keeps the user's credentials", pid)
	}
	if n := m.childCount(); n != 0 {
		t.Fatalf("child map still holds %d entries after the close", n)
	}
}
