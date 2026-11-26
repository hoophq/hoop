package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const fatalChildEnv = "HOOP_LOG_FATAL_CHILD"

// childWatchdog stops a child that hangs, so a deadlock fails the test
// instead of the package timeout.
const childWatchdog = 30 * time.Second

// hookSlack is added to the child's bound before the parent fails a hook
// run as too long. Generous: CI scheduling must not fail a correct run.
const hookSlack = 3 * time.Second

// shortBound is the bound of the children that expect the cut.
const shortBound = 200 * time.Millisecond

// TestFatalChild is the process the tests below start. It does nothing in a
// normal run.
func TestFatalChild(t *testing.T) {
	scenario := os.Getenv(fatalChildEnv)
	switch scenario {
	case "":
		t.Skip("run as a child of the fatal tests only")
	case "zap-default":
		// The hook zap installs when none is given: the exit before OnFatal.
		zlog.WithOptions(zap.WithFatalHook(zapcore.WriteThenFatal)).Sugar().Fatal("boom")
		t.Fatal("Fatal returned")
	}

	// Every other child prints how long the hook ran, measured from the
	// first Fatal call to the exit.
	var start time.Time
	exitProcess = func(code int) {
		fmt.Printf("hook %d\n", time.Since(start))
		os.Exit(code)
	}
	switch scenario {
	case "none":
	case "flush":
		OnFatal(func() { fmt.Println("flushed") })
	case "stuck":
		fatalFlushBound = shortBound
		OnFatal(func() { select {} })
		OnFatal(func() { fmt.Println("never") })
	case "panic":
		OnFatal(func() { panic("flush") })
		OnFatal(func() { fmt.Println("flushed") })
	case "nested":
		fatalFlushBound = shortBound
		OnFatal(func() { Fatal("again") })
		OnFatal(func() { fmt.Println("never") })
	case "concurrent":
		OnFatal(func() {
			time.Sleep(50 * time.Millisecond)
			fmt.Println("flushed")
		})
		var ready sync.WaitGroup
		gate := make(chan struct{})
		for range 2 {
			ready.Add(1)
			go func() {
				ready.Done()
				<-gate
				Fatal("boom")
			}()
		}
		ready.Wait()
		start = time.Now()
		close(gate)
		time.Sleep(childWatchdog)
		t.Fatal("Fatal returned")
	default:
		t.Fatalf("unknown scenario %q", scenario)
	}
	start = time.Now()
	Fatal("boom")
	t.Fatal("Fatal returned")
}

type fatalChild struct {
	// lines is stdout without the hook line.
	lines []string
	code  int
	// hook is the first hook duration the child printed; -1 for none.
	hook time.Duration
}

// runFatalChild runs one scenario under the watchdog.
func runFatalChild(t *testing.T, scenario string) fatalChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childWatchdog)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFatalChild$")
	cmd.Env = append(os.Environ(), fatalChildEnv+"="+scenario, "LOG_ENCODING=json", "LOG_LEVEL=info")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("child %s: the watchdog stopped it after %s; stdout:\n%s", scenario, childWatchdog, stdout.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("child %s: want an exit error, got %v; stdout:\n%s", scenario, err, stdout.String())
	}
	got := fatalChild{code: exitErr.ExitCode(), hook: -1}
	for _, l := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		var ns int64
		if _, err := fmt.Sscanf(l, "hook %d", &ns); err == nil {
			if got.hook < 0 {
				got.hook = time.Duration(ns)
			}
			continue
		}
		got.lines = append(got.lines, l)
	}
	return got
}

// within fails when the child's hook ran longer than bound plus hookSlack.
func (c fatalChild) within(t *testing.T, bound time.Duration) {
	t.Helper()
	if c.hook < 0 {
		t.Fatalf("the child printed no hook duration; lines %q", c.lines)
	}
	t.Logf("hook ran %s", c.hook)
	if c.hook > bound+hookSlack {
		t.Fatalf("hook ran %s, bound %s", c.hook, bound)
	}
}

// fatalEntry is a fatal JSON line without the fields that differ between
// two runs: the time, the caller and the stack.
func fatalEntry(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not a JSON log line: %q", line)
	}
	delete(m, "timestamp")
	delete(m, "logger")
	delete(m, "stacktrace")
	return m
}

func count(lines []string, s string) int {
	n := 0
	for _, l := range lines {
		if l == s {
			n++
		}
	}
	return n
}

func TestFatalWithNothingRegisteredExitsAsZapDoes(t *testing.T) {
	want := runFatalChild(t, "zap-default")
	got := runFatalChild(t, "none")
	if got.code != 1 || want.code != 1 {
		t.Fatalf("exit status: got %d, zap's default %d; want 1", got.code, want.code)
	}
	if len(got.lines) != 1 || len(want.lines) != 1 {
		t.Fatalf("want one line each, got %q and zap's default %q", got.lines, want.lines)
	}
	g, w := fatalEntry(t, got.lines[0]), fatalEntry(t, want.lines[0])
	if fmt.Sprint(g) != fmt.Sprint(w) {
		t.Fatalf("got %v, zap's default %v", g, w)
	}
	if g["level"] != "fatal" || g["msg"] != "boom" {
		t.Fatalf("unexpected entry %v", g)
	}
}

func TestFatalRunsTheFlushAfterTheEntryThenExits(t *testing.T) {
	c := runFatalChild(t, "flush")
	if c.code != 1 {
		t.Fatalf("exit status %d, want 1", c.code)
	}
	if len(c.lines) != 2 || fatalEntry(t, c.lines[0])["msg"] != "boom" || c.lines[1] != "flushed" {
		t.Fatalf("want the fatal entry, then the flush; got %q", c.lines)
	}
	c.within(t, fatalFlushBound)
}

func TestAStuckFlushIsCutAtTheBound(t *testing.T) {
	c := runFatalChild(t, "stuck")
	if c.code != 1 {
		t.Fatalf("exit status %d, want 1", c.code)
	}
	if len(c.lines) != 1 || fatalEntry(t, c.lines[0])["msg"] != "boom" {
		t.Fatalf("want the fatal entry only; got %q", c.lines)
	}
	c.within(t, shortBound)
}

func TestAPanickingFlushKeepsTheExitStatus(t *testing.T) {
	c := runFatalChild(t, "panic")
	if c.code != 1 {
		t.Fatalf("exit status %d, want 1", c.code)
	}
	if len(c.lines) != 2 || c.lines[1] != "flushed" {
		t.Fatalf("want the fatal entry, then the next flush; got %q", c.lines)
	}
	c.within(t, fatalFlushBound)
}

// A flush that logs at fatal waits for the run it is part of; the bound
// ends both.
func TestAFatalInsideAFlushExitsAtTheBound(t *testing.T) {
	c := runFatalChild(t, "nested")
	if c.code != 1 {
		t.Fatalf("exit status %d, want 1", c.code)
	}
	if len(c.lines) != 2 || fatalEntry(t, c.lines[0])["msg"] != "boom" || fatalEntry(t, c.lines[1])["msg"] != "again" {
		t.Fatalf("want both fatal entries and nothing else; got %q", c.lines)
	}
	c.within(t, shortBound)
}

// Two goroutines that fail at once run the flushes once.
func TestTwoConcurrentFatalsFlushOnce(t *testing.T) {
	c := runFatalChild(t, "concurrent")
	if c.code != 1 {
		t.Fatalf("exit status %d, want 1", c.code)
	}
	if n := count(c.lines, "flushed"); n != 1 {
		t.Fatalf("flushed %d times, want 1; got %q", n, c.lines)
	}
	c.within(t, fatalFlushBound)
}
