package services

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A full queue drops the hooks at once: the batch that fires them does not
// wait for a slow webhook.
func TestSidecarHookPoolDropsWhenFull(t *testing.T) {
	var fired atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	p := newSidecarHookPool(1, 1, func(sidecarSessionHook) {
		started <- struct{}{}
		<-release
		fired.Add(1)
	})
	hook := func(sid string) sidecarSessionHook {
		return sidecarSessionHook{OrgID: "org-1", SessionID: sid, Opened: true}
	}

	require.True(t, p.enqueue(hook("s-running")))
	<-started // the only worker holds it
	require.True(t, p.enqueue(hook("s-queued")))

	done := make(chan bool, 1)
	go func() { done <- p.enqueue(hook("s-dropped")) }()
	select {
	case queued := <-done:
		assert.False(t, queued, "the queue is full")
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue blocked on a full queue")
	}

	close(release)
	require.Eventually(t, func() bool { return fired.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Never(t, func() bool { return fired.Load() > 2 }, 100*time.Millisecond, 10*time.Millisecond,
		"the dropped hooks never run")
}
