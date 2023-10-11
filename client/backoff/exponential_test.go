package backoff

import (
	"fmt"
	"testing"
	"time"
)

func TestExponential2xMustMatchDuration(t *testing.T) {
	var got time.Duration
	backoffFn = func(d time.Duration) {
		got += d
	}

	want := time.Duration(0)
	backoffDuration := time.Duration(1)
	attempt := 1
	err := Exponential2x(func(_ time.Duration) error {
		if attempt > 15 {
			return fmt.Errorf("stop")
		}
		attempt++
		if attempt <= defaulMaxBackoff {
			backoffDuration *= 2
		}
		want += time.Second * backoffDuration
		return Errorf("backoff")
	})
	if err.Error() != "stop" {
		t.Fatalf("unexpected error=%v", err)
	}
	if got != want {
		t.Errorf("want=%v, got=%v", want, got)
	}
}
