package controller

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// barrierCloser returns from Close only after every closer has entered Close.
type barrierCloser struct {
	entered *sync.WaitGroup
	all     <-chan struct{}
}

func (c *barrierCloser) Close() error {
	c.entered.Done()
	<-c.all
	return nil
}

// Agent.Close closes the stored connections in parallel (ENG-1143). A psql
// exec Close can wait 5s for the cancel; in series, N execs take N*5s and the
// 10s shutdown window ends first.
func TestAgentCloseClosesConnectionsInParallel(t *testing.T) {
	const n = 3
	a := New(newOrderedTransport(""), nil, nil)
	var entered sync.WaitGroup
	entered.Add(n)
	all := make(chan struct{})
	for i := range n {
		a.connStore.Set(fmt.Sprintf("sid-%d", i), &barrierCloser{entered: &entered, all: all})
	}
	// A stored value that is not an io.Closer must not stop the shutdown.
	a.connStore.Set("sid-params", "not a closer")
	go func() {
		entered.Wait()
		close(all)
	}()

	done := make(chan struct{})
	go func() {
		a.Close(errors.New("test shutdown"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Agent.Close did not return: connections are closed in series")
	}
	if a.shutdownCtx.Err() == nil {
		t.Fatal("shutdown context is not cancelled after Close")
	}
}
