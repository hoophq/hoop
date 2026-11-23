package transport

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flagStream records the flag packets it takes. With block set, Send waits
// until block is closed, as a gRPC send to an agent that does not read.
type flagStream struct {
	org, name string
	block     chan struct{}
	err       error

	mu      sync.Mutex
	entered int
	got     []map[string]bool
}

func (s *flagStream) Send(p *pb.Packet) error {
	s.mu.Lock()
	s.entered++
	s.mu.Unlock()
	if s.block != nil {
		<-s.block
	}
	if s.err != nil {
		return s.err
	}
	var flags map[string]bool
	if err := json.Unmarshal(p.Spec[pb.SpecFeatureFlagsKey], &flags); err != nil {
		return err
	}
	s.mu.Lock()
	s.got = append(s.got, flags)
	s.mu.Unlock()
	return nil
}
func (s *flagStream) GetOrgID() string  { return s.org }
func (s *flagStream) AgentName() string { return s.name }

func (s *flagStream) received() []map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]bool(nil), s.got...)
}

func (s *flagStream) sends() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entered
}

// flagSource is an org's flags, changed by the test.
type flagSource struct {
	mu sync.Mutex
	on map[string]bool
}

func (f *flagSource) set(org string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[org] = on
}

func (f *flagSource) flags(org string) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]bool{"f": f.on[org]}
}

func lastFlag(s *flagStream) (bool, bool) {
	got := s.received()
	if len(got) == 0 {
		return false, false
	}
	return got[len(got)-1]["f"], true
}

// A blocked agent holds only its own send: Notify returns at once, the other
// agents get each change, and the blocked one gets the latest flags, once,
// when it reads again.
func TestFeatureFlagDeliveryDoesNotWaitOnASlowAgent(t *testing.T) {
	stuck := &flagStream{org: "org-1", name: "stuck", block: make(chan struct{})}
	healthy := &flagStream{org: "org-1", name: "healthy"}
	other := &flagStream{org: "org-2", name: "other"}
	streams := map[string][]FeatureFlagStream{"org-1": {stuck, healthy}, "org-2": {other}}
	src := &flagSource{on: map[string]bool{}}
	d := NewFeatureFlagDelivery(func(org string) []FeatureFlagStream { return streams[org] }, src.flags)

	notify := func(org string) {
		t.Helper()
		start := time.Now()
		d.Notify(org)
		require.Less(t, time.Since(start), 100*time.Millisecond, "Notify blocked")
	}

	src.set("org-1", true)
	notify("org-1")
	require.Eventually(t, func() bool { return stuck.sends() == 1 }, time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { v, ok := lastFlag(healthy); return ok && v }, time.Second, 5*time.Millisecond)

	for _, on := range []bool{false, true, false} {
		src.set("org-1", on)
		notify("org-1")
	}
	require.Eventually(t, func() bool { v, ok := lastFlag(healthy); return ok && !v }, time.Second, 5*time.Millisecond)

	src.set("org-2", true)
	notify("org-2")
	require.Eventually(t, func() bool { v, ok := lastFlag(other); return ok && v }, time.Second, 5*time.Millisecond)

	assert.Equal(t, 1, stuck.sends(), "one send in flight per stream")
	close(stuck.block)
	require.Eventually(t, func() bool { return len(stuck.received()) == 2 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, []map[string]bool{{"f": true}, {"f": false}}, stuck.received(),
		"the three changes made while it was blocked arrive as one send of the latest flags")
	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.inFlight) == 0
	}, time.Second, 5*time.Millisecond, "no goroutine stays after the sends")
}

// A send that fails ends the stream's goroutine; the next change sends again.
func TestFeatureFlagDeliveryStopsOnAFailedSend(t *testing.T) {
	broken := &flagStream{org: "org-1", name: "broken", err: errors.New("stream closed")}
	src := &flagSource{on: map[string]bool{}}
	d := NewFeatureFlagDelivery(func(string) []FeatureFlagStream { return []FeatureFlagStream{broken} }, src.flags)

	d.Notify("org-1")
	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return broken.sends() == 1 && len(d.inFlight) == 0
	}, time.Second, 5*time.Millisecond)
	d.Notify("org-1")
	require.Eventually(t, func() bool { return broken.sends() == 2 }, time.Second, 5*time.Millisecond)
}
