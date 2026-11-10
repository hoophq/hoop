package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/telemetry"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/jsonrpc"
	mcpwire "github.com/hoophq/mcpproxy/mcp"
	"github.com/hoophq/mcpproxy/session"
)

type nopSession struct{}

func (nopSession) ID() string { return "sidecar" }
func (nopSession) ToolCall(context.Context, string, []byte) daemon.MCPVerdict {
	return daemon.MCPVerdict{Allowed: true}
}
func (nopSession) Hidden(string) bool { return false }
func (nopSession) ToolList(context.Context, []daemon.MCPToolInfo, []string) daemon.MCPVerdict {
	return daemon.MCPVerdict{Allowed: true}
}
func (nopSession) Record(context.Context, string, map[string]string) {}
func (nopSession) Close(context.Context)                             {}

// A message that reaches the pipeline after its MCP session ended, after a
// reload ended it, or after the lane closed opens no sidecar session.
func TestLateMessagesOpenNoSidecarSession(t *testing.T) {
	var opened atomic.Int32
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error) {
			opened.Add(1)
			return nopSession{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	ctx := context.Background()
	open := func(id string) error {
		_, err := s.laneSession(ctx, session.New(id, inspect.Identity{}))
		return err
	}

	if err := open("ended"); err != nil {
		t.Fatal(err)
	}
	s.Emit(ctx, audit.Event{Type: audit.EventSessionEnd, Session: "ended"})
	if err := open("ended"); !errors.Is(err, errSessionEnded) {
		t.Fatalf("a message after the session ended: %v", err)
	}

	if err := open("reloaded"); err != nil {
		t.Fatal(err)
	}
	s.EndSessions("rules reloaded")
	s.Emit(ctx, audit.Event{Type: audit.EventSessionEnd, Session: "reloaded"})
	if err := open("reloaded"); !errors.Is(err, errSessionEnded) {
		t.Fatalf("a message after a reload and the end it caused: %v", err)
	}

	_ = s.Close()
	if err := open("late"); !errors.Is(err, errServerClosed) {
		t.Fatalf("a message after Close: %v", err)
	}
	if n := opened.Load(); n != 2 {
		t.Fatalf("opened %d sidecar sessions, want 2", n)
	}
}

type recordingSession struct {
	nopSession
	acts []string
}

func (r *recordingSession) Hidden(tool string) bool { return tool == "delete_customer" }
func (r *recordingSession) Record(_ context.Context, activity string, attrs map[string]string) {
	r.acts = append(r.acts, activity+": "+attrs["reason"])
}

// A tools/list result the lane cannot read ends the session: it could neither
// hide a tool from it nor judge it, so it never reaches the client as sent.
func TestAnUnreadableCatalogEndsTheSession(t *testing.T) {
	rec := &recordingSession{}
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error) { return rec, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	for _, result := range []string{
		`{"tools":[{"name":"delete_customer"},{"name":7}]}`,
		`{"tools":"none"}`,
	} {
		rec.acts = nil
		m := &inspect.Msg{Dir: inspect.S2C, CorrelatedMethod: mcpwire.MethodToolsList,
			Env: &jsonrpc.Message{ID: jsonrpc.ID{Raw: json.RawMessage("2")}, Result: json.RawMessage(result)}}
		d := (&hideCheck{s: s}).Inspect(context.Background(), session.New("m1", inspect.Identity{}), m)
		if d.Verdict != inspect.Kill || string(m.Env.Result) != result {
			t.Errorf("%s: verdict %v, result %s", result, d.Verdict, m.Env.Result)
		}
		if len(rec.acts) != 1 || !strings.HasPrefix(rec.acts[0], "mcp_catalog_unreadable") {
			t.Errorf("%s: activities %v", result, rec.acts)
		}
	}
}

type closeCounter struct {
	nopSession
	closed *atomic.Int32
}

func (c closeCounter) Close(context.Context) { c.closed.Add(1) }

// A session opens outside the server lock: a slow open stalls neither the
// open sessions nor a second open of the same session, which waits for the
// first and gets the same sidecar session. A reload during an open ends it.
func TestASlowSessionOpenStallsNothingElse(t *testing.T) {
	release := make(chan struct{})
	var opened, closed atomic.Int32
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(_ context.Context, c daemon.MCPCaller) (daemon.MCPLaneSession, error) {
			opened.Add(1)
			if c.MCPSessionID == "slow" {
				<-release
			}
			return closeCounter{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	ctx := context.Background()
	if _, err := s.laneSession(ctx, session.New("fast", inspect.Identity{})); err != nil {
		t.Fatal(err)
	}

	slow := session.New("slow", inspect.Identity{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = s.laneSession(ctx, slow) }()
	}
	for {
		s.mu.Lock()
		n := len(s.opening)
		s.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	got := make(chan error, 1)
	go func() { _, err := s.laneSession(ctx, session.New("fast", inspect.Identity{})); got <- err }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an open session waited on another session's open")
	}

	s.EndSessions("rules reloaded")
	close(release)
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, errSessionEnded) {
			t.Errorf("open %d of the slow session: %v, want errSessionEnded", i, err)
		}
	}
	if n := opened.Load(); n != 2 {
		t.Errorf("opened %d sidecar sessions, want 2 (fast, slow once)", n)
	}
	if n := closed.Load(); n != 2 {
		t.Errorf("closed %d sidecar sessions, want 2 (fast by the reload, slow after its open)", n)
	}
}

// foldKey agrees with strings.EqualFold, the match Go's JSON decoder uses for
// keys, on every rune of the basic plane and on the keys an attacker would try.
func TestFoldKeyAgreesWithEqualFold(t *testing.T) {
	for r := rune(0); r <= 0xFFFF; r++ {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if foldKey(string(r)) != foldKey(string(f)) {
				t.Fatalf("%U and %U fold together but foldKey differs", r, f)
			}
		}
	}
	for _, c := range [][2]string{{"arguments", "argumentſ"}, {"kind", "\u212Aind"}, {"name", "NAME"}, {"name", "names"}, {"ß", "ss"}} {
		if (foldKey(c[0]) == foldKey(c[1])) != strings.EqualFold(c[0], c[1]) {
			t.Errorf("%q, %q: foldKey and EqualFold disagree", c[0], c[1])
		}
	}
}

// The duplicate-key check is linear: params with many keys, as a 16 MiB body
// allows, are read in well under a second.
func TestToolCallParamsWithManyKeysIsFast(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"name":"echo"`)
	for i := range 200000 {
		fmt.Fprintf(&b, `,"k%d":0`, i)
	}
	b.WriteString("}")
	start := time.Now()
	if _, _, err := toolCallParams(json.RawMessage(b.String())); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("200000 keys took %v", d)
	}
}

// A top-level key that repeats, exactly or escaped, is refused and named;
// a repeat inside arguments is not this check's to judge.
func TestToolCallParamsRefusesARepeatedKey(t *testing.T) {
	for params, want := range map[string]string{
		`{"name":"a","name":"b"}`:                    `key "name" appears twice`,
		`{"name":"a","arguments":{},"arguments":{}}`: `key "arguments" appears twice`,
		`{"name":"a","\u006eame":"b"}`:               `key "name" appears twice`,
		`{"name":"a","Name":"b"}`:                    `differ only in case`,
		`{"name":"a"} {"name":"b"}`:                  `not an object`,
		`{"name":"a","arguments":{"x":1,"x":2}}`:     ``,
	} {
		_, _, err := toolCallParams(json.RawMessage(params))
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v, want %q", params, err, want)
		}
	}
}

// Telemetry names a tool by its real name, and at most maxAdvertised tools.
func TestToolLabelsAreBounded(t *testing.T) {
	s := &server{advertised: map[string]bool{}, renamed: map[string]string{"find_customer": "customer_lookup"}}
	names := []string{"customer_lookup"}
	for i := range maxAdvertised {
		names = append(names, fmt.Sprintf("tool_%d", i))
	}
	s.advertise(names)
	for tool, want := range map[string]string{
		"find_customer":        "customer_lookup",
		names[maxAdvertised-1]: names[maxAdvertised-1],
		names[maxAdvertised]:   telemetry.MCPOther,
		"":                     "",
	} {
		if got := s.toolLabel(tool); got != want {
			t.Errorf("toolLabel(%q) = %q, want %q", tool, got, want)
		}
	}
}
