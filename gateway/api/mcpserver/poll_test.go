package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResolveWaitTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want time.Duration
	}{
		{"zero defaults", 0, defaultWaitTimeout},
		{"negative defaults", -5, defaultWaitTimeout},
		{"under poll interval clamps up", 1, pollInterval},
		{"valid mid-range", 60, 60 * time.Second},
		{"at max", 300, maxWaitTimeout},
		{"above max clamps down", 99999, maxWaitTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWaitTimeout(tc.in)
			if got != tc.want {
				t.Errorf("resolveWaitTimeout(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestWaitUntil_AlreadyDone(t *testing.T) {
	calls := 0
	val, timedOut, waited, err := waitUntil(context.Background(), 5*time.Second, func() (string, bool, error) {
		calls++
		return "ready", true, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if val != "ready" {
		t.Errorf("expected 'ready', got %q", val)
	}
	if timedOut {
		t.Errorf("expected timedOut=false, got true")
	}
	if waited >= pollInterval {
		t.Errorf("expected near-zero wait, got %v", waited)
	}
}

func TestWaitUntil_DoneAfterFewTicks(t *testing.T) {
	calls := 0
	flipAt := 3
	val, timedOut, _, err := waitUntil(context.Background(), 30*time.Second, func() (int, bool, error) {
		calls++
		return calls, calls >= flipAt, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if calls != flipAt {
		t.Errorf("expected %d calls, got %d", flipAt, calls)
	}
	if val != flipAt {
		t.Errorf("expected val=%d, got %d", flipAt, val)
	}
	if timedOut {
		t.Errorf("expected timedOut=false")
	}
}

func TestWaitUntil_Timeout(t *testing.T) {
	calls := 0
	timeout := 3 * pollInterval // 6s — gives the timer a chance to fire after a couple of ticks
	val, timedOut, waited, err := waitUntil(context.Background(), timeout, func() (string, bool, error) {
		calls++
		return "still-pending", false, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !timedOut {
		t.Errorf("expected timedOut=true, got false (calls=%d, waited=%v)", calls, waited)
	}
	if val != "still-pending" {
		t.Errorf("expected val from final read, got %q", val)
	}
	if waited < timeout-pollInterval || waited > timeout+2*pollInterval {
		t.Errorf("expected waited near %v, got %v", timeout, waited)
	}
}

func TestWaitUntil_FnErrorPropagates(t *testing.T) {
	sentinel := errors.New("db blew up")
	val, _, _, err := waitUntil(context.Background(), 30*time.Second, func() (string, bool, error) {
		return "", false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
	if val != "" {
		t.Errorf("expected zero val, got %q", val)
	}
}

func TestWaitUntil_CtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	calls := 0
	_, timedOut, waited, err := waitUntil(ctx, 30*time.Second, func() (string, bool, error) {
		calls++
		return "pending", false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if timedOut {
		t.Errorf("expected timedOut=false on cancel")
	}
	if waited > 5*time.Second {
		t.Errorf("expected fast cancellation, waited %v", waited)
	}
}

func TestWaitUntil_FnErrorDuringPoll(t *testing.T) {
	sentinel := errors.New("transient")
	calls := 0
	_, _, _, err := waitUntil(context.Background(), 30*time.Second, func() (string, bool, error) {
		calls++
		if calls == 1 {
			return "pending", false, nil
		}
		return "", false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel from poll iteration, got %v", err)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 calls, got %d", calls)
	}
}
