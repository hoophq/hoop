// Package eventbroker is an in-memory pub/sub for live session events.
// Each subscribed connection (e.g. an SSE client) gets a buffered channel
// that receives every event written to a session's WAL in real time.
package eventbroker

import (
	"sync"
	"time"
)

// Event is a single audit event published as it is appended to a session's WAL.
type Event struct {
	Time    time.Time
	Type    string // "i" input, "o" output, "e" error
	Payload []byte
}

type subscriber struct {
	ch chan Event
}

type Broker struct {
	mu   sync.Mutex
	subs map[string]map[uint64]*subscriber
	next uint64
}

var Default = New()

func New() *Broker {
	return &Broker{subs: make(map[string]map[uint64]*subscriber)}
}

// Subscribe returns a receive channel for the given session and an idempotent
// unsubscribe func. The channel is closed when Remove is called for the session.
func (b *Broker) Subscribe(sessionID string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subs[sessionID] == nil {
		b.subs[sessionID] = make(map[uint64]*subscriber)
	}
	id := b.next
	b.next++

	sub := &subscriber{ch: make(chan Event, 32)}
	b.subs[sessionID][id] = sub

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if subs, ok := b.subs[sessionID]; ok {
				delete(subs, id)
				if len(subs) == 0 {
					delete(b.subs, sessionID)
				}
			}
		})
	}
	return sub.ch, unsubscribe
}

// Publish fans out an event to every subscriber for the session. Non-blocking:
// if a subscriber's buffer is full, the event is dropped for that subscriber.
func (b *Broker) Publish(sessionID string, ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs[sessionID] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// Remove closes every subscriber channel for the session and forgets it.
// Subscribers detect end-of-session via channel close.
func (b *Broker) Remove(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs[sessionID] {
		close(sub.ch)
	}
	delete(b.subs, sessionID)
}
