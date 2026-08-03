package log

import (
	"fmt"
	"testing"
	"time"
)

func tailContains(message string) bool {
	for _, entry := range Tail.Snapshot() {
		if entry.Message == message {
			return true
		}
	}
	return false
}

// TestTailFollowsLogLevel pins the capture-floor contract: the tail buffers
// exactly what the process log level enables — via LOG_LEVEL at construction
// and via SetDefaultLoggerLevel at runtime.
func TestTailFollowsLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	logger := NewDefaultLogger(nil)

	debugMsg := fmt.Sprintf("tail-debug-%d", time.Now().UnixNano())
	infoMsg := fmt.Sprintf("tail-info-%d", time.Now().UnixNano())
	logger.Debug(debugMsg)
	logger.Info(infoMsg)
	if tailContains(debugMsg) {
		t.Errorf("debug entry captured under LOG_LEVEL=info: %q", debugMsg)
	}
	if !tailContains(infoMsg) {
		t.Errorf("info entry not captured under LOG_LEVEL=info: %q", infoMsg)
	}

	// Runtime level change flows into the tail through the shared atomic level.
	SetDefaultLoggerLevel("DEBUG")
	defer SetDefaultLoggerLevel("INFO")
	debugMsg2 := fmt.Sprintf("tail-debug-live-%d", time.Now().UnixNano())
	logger.Debug(debugMsg2)
	if !tailContains(debugMsg2) {
		t.Errorf("debug entry not captured after SetDefaultLoggerLevel(DEBUG): %q", debugMsg2)
	}
	for _, entry := range Tail.Snapshot() {
		if entry.Message == debugMsg2 && entry.Level != "debug" {
			t.Errorf("captured entry level = %q, want debug", entry.Level)
		}
	}
}
