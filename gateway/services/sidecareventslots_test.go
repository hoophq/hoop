package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSizeSidecarEventSlotsGivesAFifthOfThePool(t *testing.T) {
	t.Cleanup(func() { SizeSidecarEventSlots(defaultSidecarEventSlots * 5) })
	for pool, want := range map[int]int{1: 1, 4: 1, 5: 1, 50: 10, 200: 40} {
		SizeSidecarEventSlots(pool)
		assert.Equal(t, want, cap(sidecarEventSlots), "ingest slots for a pool of %d", pool)
		assert.Equal(t, want, cap(sidecarHookSlots), "hook slots for a pool of %d", pool)
	}
}

func TestAdmitSidecarEventsWaitsThenRefuses(t *testing.T) {
	t.Cleanup(func() { SizeSidecarEventSlots(defaultSidecarEventSlots * 5) })
	SizeSidecarEventSlots(5)

	release, ok := AdmitSidecarEvents(context.Background(), 0)
	require.True(t, ok)
	_, ok = AdmitSidecarEvents(context.Background(), 20*time.Millisecond)
	assert.False(t, ok, "a full ingest refuses after the wait")

	go func() {
		time.Sleep(20 * time.Millisecond)
		release()
	}()
	release, ok = AdmitSidecarEvents(context.Background(), time.Second)
	require.True(t, ok, "a slot freed during the wait is taken")
	release()
}

// A hook waits for a slot, so the batch that fires it holds its ingest slot.
func TestRunSidecarSessionHooksWaitsForASlot(t *testing.T) {
	t.Cleanup(func() { SizeSidecarEventSlots(defaultSidecarEventSlots * 5) })
	SizeSidecarEventSlots(5)
	sidecarHookSlots <- struct{}{}

	started := make(chan struct{})
	go func() {
		// No flag set: the hook returns at once and reads nothing.
		runSidecarSessionHooks(sidecarSessionHook{SessionID: "s1"})
		close(started)
	}()
	select {
	case <-started:
		t.Fatal("the hook started without a slot")
	case <-time.After(50 * time.Millisecond):
	}

	<-sidecarHookSlots
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the hook did not start once a slot was free")
	}
	require.Eventually(t, func() bool { return len(sidecarHookSlots) == 0 }, time.Second, 5*time.Millisecond,
		"the hook gives its slot back")
}

// A slow webhook endpoint holds no hook slot, so it cannot hold the ingest.
func TestRunSidecarSessionHooksFreesTheSlotBeforeTheWebhooks(t *testing.T) {
	t.Cleanup(func() { SizeSidecarEventSlots(defaultSidecarEventSlots * 5) })
	SizeSidecarEventSlots(5)

	unblock := make(chan struct{})
	defer close(unblock)
	slowWebhook := func(sidecarSessionHook) func() { return func() { <-unblock } }

	runSidecarSessionHooksWith(sidecarSessionHook{SessionID: "s1"}, slowWebhook)
	done := make(chan struct{})
	go func() {
		runSidecarSessionHooksWith(sidecarSessionHook{SessionID: "s2"}, slowWebhook)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second hook waited for the first one's webhook")
	}
}

func TestRunSidecarSessionHooksDropsWebhooksPastTheBound(t *testing.T) {
	for range cap(sidecarWebhookSends) {
		sidecarWebhookSends <- struct{}{}
	}
	t.Cleanup(func() {
		for len(sidecarWebhookSends) > 0 {
			<-sidecarWebhookSends
		}
	})

	sent := make(chan struct{}, 1)
	runSidecarSessionHooksWith(sidecarSessionHook{SessionID: "s1"},
		func(sidecarSessionHook) func() { return func() { sent <- struct{}{} } })
	require.Eventually(t, func() bool { return len(sidecarHookSlots) == 0 }, time.Second, 5*time.Millisecond)
	select {
	case <-sent:
		t.Fatal("a webhook went out past the bound")
	case <-time.After(50 * time.Millisecond):
	}
}
