package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	listed   []string
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
	return daemon.ReviewStatus{ID: id, Status: status, ListenerName: "appdb", ApprovalRule: "dba"}, nil
}

// ListReviews answers the next status for one review, the way ReviewStatus
// does, and records the filter it was asked with.
func (f *fakeReviews) ListReviews(ctx context.Context, status string, limit int) ([]daemon.ReviewStatus, error) {
	f.mu.Lock()
	f.listed = append(f.listed, fmt.Sprintf("%s/%d", status, limit))
	f.mu.Unlock()
	rev, err := f.ReviewStatus(ctx, "9f97c0de-0000-0000-0000-000000000001")
	if err != nil {
		return nil, err
	}
	return []daemon.ReviewStatus{rev}, nil
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
	if strings.Join(names, ",") != "review_list,review_status,review_wait" {
		t.Errorf("tools %v, want review_list, review_status and review_wait", names)
	}
}

// The ticket's acceptance path: one review followed from PENDING to APPROVED.
func TestAnAgentFollowsOneReviewFromPendingToApproved(t *testing.T) {
	reviews := &fakeReviews{statuses: []string{statusPending, statusPending, statusPending, statusApproved}}
	cs, progress := connect(t, reviews)

	out, _ := call(t, cs, &sdk.CallToolParams{Name: "review_status", Arguments: map[string]any{"id": "r1"}})
	if out.Status != statusPending || out.Next != daemon.ReviewNextWait {
		t.Fatalf("review_status answered %+v", out)
	}

	params := &sdk.CallToolParams{Name: "review_wait", Arguments: map[string]any{"id": "r1"}}
	params.SetProgressToken("p1")
	out, _ = call(t, cs, params)
	if out.Status != statusApproved || out.Next != daemon.ReviewNextResend {
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
	if out.TimedOut == nil || !*out.TimedOut || out.Status != statusPending || out.Next != daemon.ReviewNextWait {
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
		statusPending:  daemon.ReviewNextWait,
		statusApproved: daemon.ReviewNextResend,
		statusRejected: daemon.ReviewNextStop,
		statusRevoked:  daemon.ReviewNextStop,
		statusExecuted: daemon.ReviewNextStop,
		"PROCESSING":   daemon.ReviewNextStop,
		"":             daemon.ReviewNextStop,
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

// An agent lists this sidecar's reviews and reads what to do about each.
func TestAnAgentListsTheSidecarsReviews(t *testing.T) {
	reviews := &fakeReviews{statuses: []string{statusPending}}
	cs, _ := connect(t, reviews)
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "review_list", Arguments: map[string]any{"status": "pending", "limit": 500}})
	if err != nil || res.IsError {
		t.Fatalf("review_list: %v %s", err, errorText(res))
	}
	var out listOutput
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Reviews) != 1 || out.Reviews[0].Next != daemon.ReviewNextWait {
		t.Errorf("reviews = %+v, want one pending review to wait on", out.Reviews)
	}
	if got := strings.Join(reviews.listed, ","); got != "PENDING/200" {
		t.Errorf("the plane was asked for %q, want PENDING/200: the limit is capped", got)
	}
}

// A plane without the list route sends the agent back to review_status.
func TestReviewListOnAnOldPlanePointsAtReviewStatus(t *testing.T) {
	cs, _ := connect(t, &fakeReviews{err: daemon.ErrPlaneTooOld})
	_, res := call(t, cs, &sdk.CallToolParams{Name: "review_list"})
	if !res.IsError || !strings.Contains(errorText(res), "review_status") {
		t.Errorf("result %s, want an error naming review_status", errorText(res))
	}
}
