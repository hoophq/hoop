package log

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestTailObserverFollowsLogLevel pins the capture contract: registered
// observers receive exactly what the process log level enables — via
// LOG_LEVEL at construction and via SetDefaultLoggerLevel at runtime.
func TestTailObserverFollowsLogLevel(t *testing.T) {
	var mu sync.Mutex
	var captured []TailEntry
	TailObserve(func(e TailEntry) {
		mu.Lock()
		captured = append(captured, e)
		mu.Unlock()
	})
	contains := func(message string) *TailEntry {
		mu.Lock()
		defer mu.Unlock()
		for i := range captured {
			if captured[i].Message == message {
				return &captured[i]
			}
		}
		return nil
	}

	t.Setenv("LOG_LEVEL", "info")
	logger := NewDefaultLogger(nil)

	debugMsg := fmt.Sprintf("tail-debug-%d", time.Now().UnixNano())
	infoMsg := fmt.Sprintf("tail-info-%d", time.Now().UnixNano())
	logger.Debug(debugMsg)
	logger.Info(infoMsg)
	if contains(debugMsg) != nil {
		t.Errorf("debug entry captured under LOG_LEVEL=info: %q", debugMsg)
	}
	entry := contains(infoMsg)
	if entry == nil {
		t.Fatalf("info entry not captured under LOG_LEVEL=info: %q", infoMsg)
	}
	if entry.Level != "info" || entry.Timestamp.IsZero() {
		t.Errorf("captured entry malformed: %+v", entry)
	}

	// Runtime level change flows into capture through the shared atomic level.
	SetDefaultLoggerLevel("DEBUG")
	defer SetDefaultLoggerLevel("INFO")
	debugMsg2 := fmt.Sprintf("tail-debug-live-%d", time.Now().UnixNano())
	logger.Debug(debugMsg2)
	entry = contains(debugMsg2)
	if entry == nil {
		t.Fatalf("debug entry not captured after SetDefaultLoggerLevel(DEBUG): %q", debugMsg2)
	}
	if entry.Level != "debug" {
		t.Errorf("captured entry level = %q, want debug", entry.Level)
	}
}
