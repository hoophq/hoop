package daemon

import (
	"strings"
	"testing"
)

// A document this process refused is still refused on the next tick. The
// bytes did not change, so nothing is re-run and nothing is re-logged, but
// the outcome the heartbeat reports must not decay into "unchanged": the
// plane would read that as the sidecar having taken the document (ADR-0022).
func TestARefusedDocumentStaysRefusedUntilAnotherApplies(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	unknown := editJSON(t, reloadBase, `"log_level": "info"`, `"log_level": "info", "future_key": true`)
	if got := handleWith(rl, buf, unknown); got != reloadRefused {
		t.Fatalf("first handle = %v, want refused; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, `unknown field "future_key"`) {
		t.Errorf("the reason does not name the key: %q", rl.lastReason)
	}
	logged := strings.Count(buf.String(), "this build refuses")
	if got := handleWith(rl, buf, unknown); got != reloadRefused {
		t.Fatalf("second handle = %v, want refused again; log:\n%s", got, buf)
	}
	if strings.Count(buf.String(), "this build refuses") != logged {
		t.Errorf("the same document was logged again:\n%s", buf)
	}
	if rl.lastReason == "" {
		t.Error("the reason was dropped on the repeat")
	}

	fixed := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := handleWith(rl, buf, fixed); got != reloadApplied {
		t.Fatalf("a good document after a refusal = %v, want applied; log:\n%s", got, buf)
	}
	if rl.lastReason != "" {
		t.Errorf("an applied document kept the old reason: %q", rl.lastReason)
	}
	if got := handleWith(rl, buf, fixed); got != reloadUnchanged {
		t.Fatalf("repeat of an applied document = %v, want unchanged", got)
	}
}

// Restart-bound drift is reported the same way: the process still runs the
// previous document, and will until someone restarts it.
func TestARestartBoundDocumentStaysRestartUntilAnotherApplies(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	beyond := editJSON(t, reloadBase, `"log_level": "info"`, `"log_level": "debug"`)
	if got := handleWith(rl, buf, beyond); got != reloadRestart {
		t.Fatalf("first handle = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(rl.lastReason, "restart to apply it") {
		t.Errorf("the reason does not say to restart: %q", rl.lastReason)
	}
	if got := handleWith(rl, buf, beyond); got != reloadRestart {
		t.Fatalf("second handle = %v, want restart again; log:\n%s", got, buf)
	}
}
