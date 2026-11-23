package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
)

// testRunningReloader is testReloader with the lanes served: every lane is
// built and bound through a laneRuntime on 127.0.0.1:0, so a document that
// adds or removes a listener has sockets to open and close.
func testRunningReloader(t *testing.T, raw string) (*reloader, *bytes.Buffer, *laneRuntime) {
	t.Helper()
	cfg, err := LoadConfigBytes([]byte(raw))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	cfg.cp = &controlPlane{url: "http://plane", cred: tokenCredential("hsc_x"), lastRaw: []byte(raw),
		license: cfg.License, licenseManaged: true}
	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	log := slog.New(slog.DiscardHandler)
	rt := &laneRuntime{cfg: cfg, log: log, set: &laneSet{}, tokens: &tokenWatch{},
		ctx: ctx, wg: &wg, fail: func(err error) { panic("listener failed: " + err.Error()) }}
	swappers := map[string]ruleSwapper{}
	for i := range lanes {
		rl, berr := rt.build(&lanes[i])
		if berr == nil {
			berr = rt.start(rl)
		}
		if berr != nil {
			t.Fatalf("start %s: %v", lanes[i].name, berr)
		}
		swappers[rl.name] = rl.rules
	}
	t.Cleanup(func() {
		cancel()
		rt.set.closeAll(log)
		wg.Wait()
	})
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, swappers, nil, nil, view, newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	rl.rt = rt
	var buf bytes.Buffer
	return rl, &buf, rt
}

func laneAddr(t *testing.T, rt *laneRuntime, name string) string {
	t.Helper()
	for _, l := range rt.set.snapshot() {
		if l.name == name {
			if l.addr() == nil {
				t.Fatalf("lane %q has no address", name)
			}
			return l.addr().String()
		}
	}
	t.Fatalf("lane %q is not running; running: %v", name, laneNames(rt))
	return ""
}

func laneNames(rt *laneRuntime) []string {
	var out []string
	for _, l := range rt.set.snapshot() {
		out = append(out, l.name)
	}
	return out
}

// stillOpen reports whether the peer kept c: a read times out, with no
// EOF.
func stillOpen(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return errorsAs(err, &ne) && ne.Timeout()
}

func errorsAs(err error, target *net.Error) bool {
	ne, ok := err.(net.Error)
	if ok {
		*target = ne
	}
	return ok
}

// holdingUpstream is an upstream that accepts and keeps every connection
// until the test ends, so a relay connection stays open across a reload
// instead of dying on a dial to a host that does not exist.
func holdingUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range held {
			c.Close()
		}
		mu.Unlock()
	})
	return ln.Addr().String()
}

// withUpstream points reloadBase's lane at a real upstream.
func withUpstream(t *testing.T, doc string) string {
	t.Helper()
	return editJSON(t, doc, `"upstream": "h:5432"`, fmt.Sprintf(`"upstream": %q`, holdingUpstream(t)))
}

// secondListener is a second postgres lane on a port of its own: Validate
// refuses two listeners on one address, port 0 included.
func secondListener(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{"name": "second", "protocol": "postgres", "listen": %q, "upstream": "h2:5432"}, `, freeTCPAddr(t))
}

// unixSocketPath is a socket path short enough for macOS's 104-byte limit;
// t.TempDir is not.
func unixSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "uds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "pg.sock")
}

// A listener with a new name starts in place: its socket binds and accepts
// while the running lane keeps its socket and its open connections.
func TestAnAddedListenerStartsInPlace(t *testing.T) {
	base := withUpstream(t, reloadBase)
	rl, buf, rt := testRunningReloader(t, base)
	first := laneAddr(t, rt, "appdb")
	open, err := net.Dial("tcp", first)
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	// The client sends nothing: the relay waits for its startup message,
	// and the held upstream keeps the other side open.
	if !stillOpen(open) {
		t.Fatal("test bug: the connection did not survive before the reload")
	}

	drifted := editJSON(t, base, `"listeners": [{`, `"listeners": [`+secondListener(t)+`{`)
	if got := handleWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if rl.gen != 1 {
		t.Errorf("generation = %d, want 1", rl.gen)
	}
	if !strings.Contains(buf.String(), `msg="listener started" listener=second`) {
		t.Errorf("no started line:\n%s", buf)
	}
	c, err := net.Dial("tcp", laneAddr(t, rt, "second"))
	if err != nil {
		t.Fatalf("the added listener does not accept: %v", err)
	}
	c.Close()
	if got := laneAddr(t, rt, "appdb"); got != first {
		t.Errorf("the running lane rebound: %s, was %s", got, first)
	}
	if !stillOpen(open) {
		t.Error("the running lane's connection was closed by the reload")
	}
	if got := len(rl.view.Load().lanes); got != 2 {
		t.Errorf("/config renders %d lanes, want 2", got)
	}
	if _, ok := rl.servers["second"]; !ok {
		t.Error("the added lane has no swap target; the next rule edit would skip it")
	}
	// The reloader compares the next document against the set that now
	// runs, so the same bytes read as unchanged.
	if got := handleWith(rl, buf, drifted); got != reloadUnchanged {
		t.Errorf("second pass = %v, want unchanged", got)
	}
}

// A listener whose name is gone stops: its socket closes, its connections
// end, and the other lane keeps its socket and connections.
func TestARemovedListenerStopsInPlace(t *testing.T) {
	base := withUpstream(t, reloadBase)
	two := editJSON(t, base, `"listeners": [{`, `"listeners": [`+secondListener(t)+`{`)
	rl, buf, rt := testRunningReloader(t, two)
	first := laneAddr(t, rt, "appdb")
	secondAddr := laneAddr(t, rt, "second")
	onFirst, err := net.Dial("tcp", first)
	if err != nil {
		t.Fatal(err)
	}
	defer onFirst.Close()
	onSecond, err := net.Dial("tcp", secondAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer onSecond.Close()

	if got := handleWith(rl, buf, base); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), `msg="listener stopped" listener=second`) {
		t.Errorf("no stopped line:\n%s", buf)
	}
	if names := laneNames(rt); len(names) != 1 || names[0] != "appdb" {
		t.Errorf("running lanes = %v, want [appdb]", names)
	}
	if c, err := net.DialTimeout("tcp", secondAddr, time.Second); err == nil {
		c.Close()
		t.Error("the removed listener still accepts")
	}
	if stillOpen(onSecond) {
		t.Error("a connection on the removed lane survived")
	}
	if !stillOpen(onFirst) {
		t.Error("a connection on the kept lane was closed")
	}
	for _, m := range []map[string][]byte{rl.laneDocs, rl.listeners} {
		if _, ok := m["second"]; ok {
			t.Error("the removed lane is still in the reloader's books")
		}
	}
	if _, ok := rl.servers["second"]; ok {
		t.Error("the removed lane still has a swap target")
	}
}

// The reloader refuses, as a whole, a document whose added listener cannot
// bind: the running lanes keep their rules, no added lane runs, and the
// reason names the listener and the bind error.
func TestAnAddedListenerOnAHeldPortRefusesTheWholeDocument(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rl, buf, rt := testRunningReloader(t, reloadBase)
	before := rl.laneDocs["appdb"]

	drifted := editJSON(t, reloadBase, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "second", "protocol": "postgres", "listen": %q, "upstream": "h2:5432"}, {`, held.Addr().String()))
	// A rule edit rides along, to prove it does not land either.
	drifted = editJSON(t, drifted, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := handleWith(rl, buf, drifted); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
	for _, want := range []string{`listener "second" could not start`, "address already in use"} {
		if !strings.Contains(rl.lastReason, want) {
			t.Errorf("reason %q lacks %q", rl.lastReason, want)
		}
	}
	if rl.gen != 0 {
		t.Errorf("generation = %d, want 0", rl.gen)
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Errorf("running lanes = %v, want only appdb", names)
	}
	if !bytes.Equal(rl.laneDocs["appdb"], before) {
		t.Error("the rule edit in a refused document reached the running lane")
	}
	if len(rt.tokens.all()) != 0 {
		t.Error("a token file was left in the watcher")
	}
}

// A listener set that changes while a listener has no name is restart-bound:
// unnamed lanes are keyed by position, so an insert would rename them.
func TestAnUnnamedListenerKeepsASetChangeOnTheRestartPath(t *testing.T) {
	unnamed := editJSON(t, reloadBase, `"name": "appdb", `, ``)
	rl, buf, rt := testRunningReloader(t, unnamed)

	drifted := editJSON(t, unnamed, `"listeners": [{`, `"listeners": [`+secondListener(t)+`{`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, "has no name") {
		t.Errorf("reason = %q", rl.lastReason)
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Errorf("running lanes = %v, want one", names)
	}
}

// A renamed listener is a remove and an add on one address. Neither order
// is safe in place, so it stays restart-bound and the reason says rename.
func TestARenamedListenerKeepsTheRestartPath(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held.Close()
	pinned := editJSON(t, reloadBase, `"listen": "127.0.0.1:0"`, fmt.Sprintf(`"listen": %q`, held.Addr().String()))
	rl, buf, _ := testRunningReloader(t, pinned)

	drifted := editJSON(t, pinned, `"name": "appdb"`, `"name": "appdb2"`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `renamed listener "appdb" to "appdb2"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
}

// Editing a running listener's own fields is still a restart, and the reason
// names the listener, so an operator with twenty lanes knows which one.
func TestAChangedListenerNamesItselfOnTheRestartPath(t *testing.T) {
	rl, buf, _ := testRunningReloader(t, reloadBase)
	drifted := editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `changed listener "appdb"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
}

// A unix socket lane binds a path on add and unlinks it on remove; Listen
// reclaims a dead socket file an unclean shutdown left at the path.
func TestAUnixSocketListenerIsAddedAndRemovedInPlace(t *testing.T) {
	path := unixSocketPath(t)
	// A socket nothing answers on: listen, keep the file, close the fd.
	dead, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	dead.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("test bug: the dead socket file is gone: %v", err)
	}
	rl, buf, rt := testRunningReloader(t, reloadBase)

	withSock := editJSON(t, reloadBase, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "sock", "protocol": "postgres", "network": "unix", "listen": %q, "upstream": "h2:5432"}, {`, path))
	if got := handleWith(rl, buf, withSock); got != reloadApplied {
		t.Fatalf("add: outcome = %v, want applied; log:\n%s", got, buf)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("the added unix lane does not accept: %v", err)
	}
	c.Close()
	if got := laneAddr(t, rt, "sock"); got != path {
		t.Errorf("addr = %q, want %q", got, path)
	}

	if got := handleWith(rl, buf, reloadBase); got != reloadApplied {
		t.Fatalf("remove: outcome = %v, want applied; log:\n%s", got, buf)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the socket file outlived the lane: stat = %v", err)
	}
}

// A unix socket path another process answers on is a bind failure: the
// reloader refuses the document and the other process keeps the socket.
func TestAUnixSocketAnotherProcessHoldsIsRefused(t *testing.T) {
	path := unixSocketPath(t)
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	rl, buf, _ := testRunningReloader(t, reloadBase)
	withSock := editJSON(t, reloadBase, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "sock", "protocol": "postgres", "network": "unix", "listen": %q, "upstream": "h2:5432"}, {`, path))
	if got := handleWith(rl, buf, withSock); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, "live socket") {
		t.Errorf("reason = %q", rl.lastReason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the other process's socket was removed: %v", err)
	}
}

// An endpoint lane (grpc) starts the same way: its server binds inside
// Serve, start waits for the address, and the next RPC reaches it.
func TestAnAddedGRPCListenerStartsInPlace(t *testing.T) {
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()
	// The free tier enforces one guardrail rule per process; the grpc
	// lane gets it, so the running lane carries none.
	noRule := `{
  "listeners": [{"name": "appdb", "protocol": "postgres", "listen": "127.0.0.1:0", "upstream": "h:5432"}],
  "audit": {"file": "-"}
}`
	rl, buf, rt := testRunningReloader(t, noRule)

	drifted := editJSON(t, noRule, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "g", "protocol": "grpc", "listen": %q, "upstream": %q,
		  "guardrails": {"mode": "enforce", "rules": [{"name": "r0", "type": "deny_words_list", "words": ["echo/say"]}]}}, {`, freeTCPAddr(t), upstreamAddr))
	if got := handleWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr(t, rt, "g")+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0, nil)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	status := resp.Header.Get("Grpc-Status")
	if status == "" {
		status = resp.Trailer.Get("Grpc-Status")
	}
	if status != "7" {
		t.Fatalf("grpc-status = %q, want 7 from the added lane's rule", status)
	}

	if got := handleWith(rl, buf, noRule); got != reloadApplied {
		t.Fatalf("remove: outcome = %v, want applied; log:\n%s", got, buf)
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Errorf("running lanes = %v, want [appdb]", names)
	}
}

// The config file is the other owner, and it gets the same behaviour: an
// edit that adds a listener starts it on the next tick.
func TestAStandaloneFileEditAddsAListener(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	rt := &laneRuntime{cfg: rl.running, log: slog.New(slog.DiscardHandler), set: &laneSet{}, tokens: &tokenWatch{},
		ctx: ctx, wg: &wg, fail: func(err error) { panic("listener failed: " + err.Error()) }}
	t.Cleanup(func() { cancel(); rt.set.closeAll(rt.log); wg.Wait() })
	rl.rt = rt

	editFile(t, path, editJSON(t, reloadBase, `"listeners": [{`, `"listeners": [`+secondListener(t)+`{`))
	pollWith(rl, buf)
	if rl.gen != 1 {
		t.Fatalf("generation = %d, want 1; log:\n%s", rl.gen, buf)
	}
	c, err := net.Dial("tcp", laneAddr(t, rt, "second"))
	if err != nil {
		t.Fatalf("the added listener does not accept: %v", err)
	}
	c.Close()
}

// Validate accepts two listeners under one name, and the registry keys a
// lane by its name. Dropping one of them must not read as "nothing
// changed" and leave its socket open: it stays restart-bound.
func TestTwoListenersUnderOneNameKeepTheRestartPath(t *testing.T) {
	twins := editJSON(t, reloadBase, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "appdb", "protocol": "postgres", "listen": %q, "upstream": "h2:5432"}, {`, freeTCPAddr(t)))
	rl, buf, rt := testRunningReloader(t, twins)

	if got := handleWith(rl, buf, reloadBase); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `changed listener "appdb"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
	if names := laneNames(rt); len(names) != 2 {
		t.Errorf("running lanes = %v, want both", names)
	}
}

func TestTwoAddedListenersUnderOneNameKeepTheRestartPath(t *testing.T) {
	rl, buf, rt := testRunningReloader(t, reloadBase)
	twin := func() string {
		return fmt.Sprintf(`{"name": "twin", "protocol": "postgres", "listen": %q, "upstream": "h2:5432"}, `, freeTCPAddr(t))
	}
	drifted := editJSON(t, reloadBase, `"listeners": [{`, `"listeners": [`+twin()+twin()+`{`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `added two listeners named "twin"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Errorf("running lanes = %v, want only appdb", names)
	}
}

// An omitted network is tcp. A rename that also spells it out is still a
// rename, not an added lane that fails its bind on the address it holds.
func TestARenameThatSpellsOutTheNetworkIsStillARename(t *testing.T) {
	pinned := editJSON(t, reloadBase, `"listen": "127.0.0.1:0"`, fmt.Sprintf(`"listen": %q`, freeTCPAddr(t)))
	rl, buf, _ := testRunningReloader(t, pinned)

	drifted := editJSON(t, pinned, `"name": "appdb"`, `"name": "appdb2", "network": "tcp"`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `renamed listener "appdb" to "appdb2"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
}

// An ica engine loads its runtime for minutes before it binds. A reload
// does not wait that long, so an added ica listener is restart-bound.
func TestAnAddedICAListenerKeepsTheRestartPath(t *testing.T) {
	rl, buf, rt := testRunningReloader(t, reloadBase)
	drifted := editJSON(t, reloadBase, `"listeners": [{`, `"listeners": [`+icaSingle+`, {`)
	if got := handleWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `added ica listener "vdi"`) {
		t.Errorf("reason = %q", rl.lastReason)
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Errorf("running lanes = %v, want only appdb", names)
	}
}

// A lane that could not bind is not running, so the token watcher must not
// keep re-reading its caller_token file.
func TestARefusedListenerLeavesNoTokenFileWatched(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tokens := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(tokens, []byte(goodToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rl, buf, rt := testRunningReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"listeners": [{`,
		fmt.Sprintf(`"listeners": [{"name": "agents", "protocol": "http", "listen": %q, "upstream": "h:1",
		  "caller_token": {"file": %q, "name": "agents"}}, {`, held.Addr().String(), tokens))
	if got := handleWith(rl, buf, drifted); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
	if n := len(rt.tokens.all()); n != 0 {
		t.Errorf("the watcher holds %d token file(s) of a lane that never started", n)
	}
}

// A startup endpoint lane binds inside Serve, and an ica lane loads its
// runtime for minutes first: start must not wait for the bind. stop ends
// the lane's own context, so a Serve that only watches its context returns.
func TestAStartupEndpointLaneDoesNotWaitForItsBind(t *testing.T) {
	var wg sync.WaitGroup
	rt := &laneRuntime{log: slog.New(slog.DiscardHandler), set: &laneSet{}, tokens: &tokenWatch{},
		ctx: context.Background(), wg: &wg, fail: func(err error) { t.Errorf("listener failed: %v", err) }}
	// fakeICAServer never publishes an address and serves until its
	// context ends.
	rl := runningLane{name: "vdi", protocol: "ica", endpoint: &fakeICAServer{}, stopped: new(atomic.Bool)}

	started := make(chan error, 1)
	go func() { started <- rt.start(rl) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start waited for the lane to bind")
	}
	if names := laneNames(rt); len(names) != 1 {
		t.Fatalf("running lanes = %v, want [vdi]", names)
	}

	if !rt.stop("vdi") {
		t.Fatal("stop found no lane")
	}
	joined := make(chan struct{})
	go func() { wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("stop left the lane's Serve running")
	}
}

// A removed lane takes its lifetime totals out of the per-protocol sum.
// The next usage window must count that lane's last connections and must
// not go negative for the lanes that stay, the protocol's last lane included.
func TestUsageCountsARemovedLanesLastWindow(t *testing.T) {
	tel := &telemetry{client: &analytics.Client{}, counters: analytics.NewCounters(), conns: map[string]connSnapshot{}}
	a, b := &fakeMCPServer{total: 100, denied: 10}, &fakeMCPServer{total: 50}
	set := &laneSet{lanes: []runningLane{
		{name: "a", protocol: "postgres", endpoint: a},
		{name: "b", protocol: "postgres", endpoint: b},
	}}
	window := func() (protoUsage, bool) {
		byProto, _ := tel.usageProperties(set.statSources(), nil)["by-protocol"].(map[string]protoUsage)
		row, ok := byProto["postgres"]
		return row, ok
	}
	if row, _ := window(); row.Connections != 150 {
		t.Fatalf("first window connections = %d, want 150", row.Connections)
	}

	a.total, a.denied, b.total = 107, 12, 51
	if _, ok := tel.retireLane(set, "a"); !ok {
		t.Fatal("retireLane found no lane")
	}
	if row, _ := window(); row.Connections != 8 || row.ConnectionsDenied != 2 {
		t.Fatalf("after a removal: %+v, want 8 connections (7 from the removed lane) and 2 denied", row)
	}

	b.total = 54
	tel.retireLane(set, "b")
	if row, _ := window(); row.Connections != 3 {
		t.Fatalf("after the last lane's removal: %+v, want 3 connections", row)
	}
	if row, ok := window(); ok {
		t.Fatalf("a protocol with no lane left still reports %+v", row)
	}
}
