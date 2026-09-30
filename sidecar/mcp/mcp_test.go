package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeReviews answers the statuses in order, repeating the last one, and
// records every id it was asked about.
type fakeReviews struct {
	mu       sync.Mutex
	statuses []string
	err      error
	asked    []string
	// expiresAt is on every answer.
	expiresAt *time.Time
}

func (f *fakeReviews) ReviewStatus(_ context.Context, id string) (daemon.ReviewStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	if f.err != nil {
		return daemon.ReviewStatus{}, f.err
	}
	status := f.statuses[0]
	if len(f.statuses) > 1 {
		f.statuses = f.statuses[1:]
	}
	return daemon.ReviewStatus{ID: id, Status: status, ListenerName: "appdb", ApprovalRule: "dba",
		ExpiresAt: f.expiresAt}, nil
}

// connect serves the handler over real streamable HTTP and returns a client
// session and the progress notifications it received.
func connect(t *testing.T, reviews daemon.ReviewStatusReader) (*sdk.ClientSession, func() int) {
	t.Helper()
	srv := httptest.NewServer(newHandler(&tools{reviews: reviews, poll: 10 * time.Millisecond}))
	t.Cleanup(srv.Close)

	var mu sync.Mutex
	progress := 0
	client := sdk.NewClient(&sdk.Implementation{Name: "test"}, &sdk.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *sdk.ProgressNotificationClientRequest) {
			mu.Lock()
			progress++
			mu.Unlock()
		},
	})
	cs, err := client.Connect(context.Background(),
		&sdk.StreamableClientTransport{Endpoint: srv.URL + Path, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, func() int { mu.Lock(); defer mu.Unlock(); return progress }
}

func call(t *testing.T, cs *sdk.ClientSession, params *sdk.CallToolParams) (reviewOutput, *sdk.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	var out reviewOutput
	if !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, res
}

func errorText(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestAClientListsBothTools(t *testing.T) {
	cs, _ := connect(t, &fakeReviews{statuses: []string{statusPending}})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
	}
	if strings.Join(names, ",") != "review_status,review_wait" {
		t.Errorf("tools %v, want review_status and review_wait", names)
	}
}

// The ticket's acceptance path: one review followed from PENDING to APPROVED.
func TestAnAgentFollowsOneReviewFromPendingToApproved(t *testing.T) {
	reviews := &fakeReviews{statuses: []string{statusPending, statusPending, statusPending, statusApproved}}
	cs, progress := connect(t, reviews)

	out, _ := call(t, cs, &sdk.CallToolParams{Name: "review_status", Arguments: map[string]any{"id": "r1"}})
	if out.Status != statusPending || out.Next != nextWait {
		t.Fatalf("review_status answered %+v", out)
	}

	params := &sdk.CallToolParams{Name: "review_wait", Arguments: map[string]any{"id": "r1"}}
	params.SetProgressToken("p1")
	out, _ = call(t, cs, params)
	if out.Status != statusApproved || out.Next != nextResend {
		t.Fatalf("review_wait answered %+v", out)
	}
	if out.TimedOut == nil || *out.TimedOut {
		t.Errorf("a decided wait reported timed_out=%v", out.TimedOut)
	}
	if !strings.Contains(out.Instruction, "identical statement") {
		t.Errorf("instruction %q does not tell the agent to resend the identical statement", out.Instruction)
	}
	if progress() == 0 {
		t.Error("a wait that polled sent no progress notification")
	}
	for _, id := range reviews.asked {
		if id != "r1" {
			t.Errorf("asked about %q", id)
		}
	}
}

// A timeout is an answer, not a failure: the agent calls again.
func TestAWaitThatTimesOutIsNotAnError(t *testing.T) {
	cs, _ := connect(t, &fakeReviews{statuses: []string{statusPending}})
	out, res := call(t, cs, &sdk.CallToolParams{Name: "review_wait",
		Arguments: map[string]any{"id": "r1", "timeout_seconds": 1}})
	if res.IsError {
		t.Fatalf("a timeout was an error: %s", errorText(res))
	}
	if out.TimedOut == nil || !*out.TimedOut || out.Status != statusPending || out.Next != nextWait {
		t.Errorf("a timed-out wait answered %+v", out)
	}
}

func TestAMissingReviewAndAnOldPlaneReadDifferently(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{daemon.ErrReviewNotFound, "not found on this sidecar"},
		{daemon.ErrPlaneTooOld, "older than this sidecar"},
	} {
		for _, tool := range []string{"review_status", "review_wait"} {
			cs, _ := connect(t, &fakeReviews{err: tc.err})
			_, res := call(t, cs, &sdk.CallToolParams{Name: tool, Arguments: map[string]any{"id": "r1"}})
			if !res.IsError || !strings.Contains(errorText(res), tc.want) {
				t.Errorf("%s with %v: error=%v text %q, want %q", tool, tc.err, res.IsError, errorText(res), tc.want)
			}
		}
	}
}

func TestEveryStatusSaysWhatToDoNext(t *testing.T) {
	for status, next := range map[string]string{
		statusPending:  nextWait,
		statusApproved: nextResend,
		statusRejected: nextStop,
		statusRevoked:  nextStop,
		statusExecuted: nextStop,
		statusExpired:  nextStop,
		"PROCESSING":   nextStop,
		"":             nextStop,
	} {
		out := describe(daemon.ReviewStatus{Status: status})
		if out.Next != next || out.Instruction == "" {
			t.Errorf("%q: next=%q instruction=%q, want next=%q", status, out.Next, out.Instruction, next)
		}
	}
	out := describe(daemon.ReviewStatus{Status: statusRejected, RejectionReason: "not in business hours"})
	if !strings.Contains(out.Instruction, "not in business hours") {
		t.Errorf("the rejection reason is missing from %q", out.Instruction)
	}
}

// An expiry ends the wait like a refusal: waiting longer never releases it,
// and a resend would page the approvers again.
func TestAnExpiredReviewEndsTheWait(t *testing.T) {
	reviews := &fakeReviews{statuses: []string{statusPending, statusExpired}}
	cs, _ := connect(t, reviews)
	out, res := call(t, cs, &sdk.CallToolParams{Name: "review_wait",
		Arguments: map[string]any{"id": "r1", "timeout_seconds": 5}})
	if res.IsError {
		t.Fatalf("an expiry was an error: %s", errorText(res))
	}
	if out.Status != statusExpired || out.Next != nextStop || out.TimedOut == nil || *out.TimedOut {
		t.Fatalf("review_wait answered %+v, want a stop on EXPIRED", out)
	}
	if !strings.Contains(out.Instruction, "only if a human asks") {
		t.Errorf("instruction %q lets the agent resend on its own", out.Instruction)
	}
}

// The deadline reaches the agent in UTC, whatever the zone the plane used.
func TestTheInstructionNamesTheDeadline(t *testing.T) {
	at := time.Date(2026, 9, 29, 15, 4, 5, 0, time.FixedZone("x", -3*3600))
	for status, want := range map[string]string{
		statusPending:  "It expires at 2026-09-29T18:04:05Z if nobody decides.",
		statusApproved: "Resend it before 2026-09-29T18:04:05Z, when the approval expires.",
	} {
		out := describe(daemon.ReviewStatus{Status: status, ExpiresAt: &at})
		if !strings.HasSuffix(out.Instruction, want) {
			t.Errorf("%s: instruction %q, want it to end with %q", status, out.Instruction, want)
		}
		if out.ExpiresAt == nil || !out.ExpiresAt.Equal(at) {
			t.Errorf("%s: expires_at %v, want %v", status, out.ExpiresAt, at)
		}
		plain := describe(daemon.ReviewStatus{Status: status})
		if strings.Contains(plain.Instruction, "expire") || plain.ExpiresAt != nil {
			t.Errorf("%s with no limit: %+v names a deadline", status, plain)
		}
	}
}

func TestTheWaitTimeoutIsClamped(t *testing.T) {
	for in, want := range map[int]time.Duration{
		-1:   defaultWaitTimeout,
		0:    defaultWaitTimeout,
		1:    pollInterval,
		90:   90 * time.Second,
		3600: maxWaitTimeout,
		// Would overflow time.Duration if multiplied before the cap.
		9223372037:  maxWaitTimeout,
		math.MaxInt: maxWaitTimeout,
	} {
		if got := resolveWaitTimeout(in); got != want {
			t.Errorf("resolveWaitTimeout(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestServeFailsOnABusyPortAndStopsWithItsContext(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := Serve(context.Background(), busy.Addr().String(), &fakeReviews{}, log); err == nil {
		t.Error("serving on a busy port succeeded")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "127.0.0.1:0", &fakeReviews{}, log) }()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled server returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop with its context")
	}
}

// Shutdown does not cancel requests on its own. Without the run context on
// them, a wait in flight would keep polling the plane after the process was
// told to stop.
func TestShutdownStopsAWaitInFlight(t *testing.T) {
	reviews := &fakeReviews{statuses: []string{statusPending}}
	asked := func() int { reviews.mu.Lock(); defer reviews.mu.Unlock(); return len(reviews.asked) }

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, ln, newHandler(&tools{reviews: reviews, poll: 10 * time.Millisecond, life: ctx}),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: "http://" + ln.Addr().String() + Path, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	go func() {
		_, _ = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "review_wait",
			Arguments: map[string]any{"id": "r1", "timeout_seconds": 300}})
	}()

	for deadline := time.Now().Add(5 * time.Second); asked() < 3; {
		if time.Now().After(deadline) {
			t.Fatal("the wait never started polling")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return: a request outlived shutdown")
	}
	after := asked()
	time.Sleep(100 * time.Millisecond)
	if n := asked(); n != after {
		t.Errorf("the wait polled %d more times after shutdown", n-after)
	}
}
