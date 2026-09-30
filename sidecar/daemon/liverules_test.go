package daemon

import "testing"

// A swapped-out generation drains only when its LAST open gate closes, once,
// and a gate opened after the swap holds the new generation, not the old.
func TestARetiredRuleGenerationDrainsAfterItsLastGate(t *testing.T) {
	rules := newLiveRules(lane{})
	first, releaseA := rules.acquire()
	_, releaseB := rules.acquire()

	drained := 0
	rules.swapLane(lane{}, func() { drained++ })
	second, releaseC := rules.acquire()
	if second == first {
		t.Fatal("a gate opened after the swap got the old generation")
	}

	releaseA()
	releaseC() // the new generation's gate; it must not drain the old one
	if drained != 0 {
		t.Fatalf("drained with a gate still open (%d)", drained)
	}
	releaseB()
	releaseB() // a second release of one gate must not count twice
	if drained != 1 {
		t.Fatalf("drained %d times after the last gate closed, want 1", drained)
	}

	// No gate open: the swap drains the outgoing generation at once.
	idle := 0
	rules.swapLane(lane{}, func() { idle++ })
	if idle != 1 {
		t.Fatalf("an idle generation drained %d times on swap, want 1", idle)
	}
}
