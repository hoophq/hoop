package interactionbroker

import "sync"

type EventType int

const (
	InteractionCreated EventType = iota
	SessionEnded
)

type InteractionEvent struct {
	Type     EventType
	Sequence int
}

type subscriber struct {
	ch chan InteractionEvent
}

type Broker struct {
	mu   sync.Mutex
	subs map[string]map[uint64]*subscriber
	next uint64
}

var Default = New()

func New() *Broker {
	return &Broker{
		subs: make(map[string]map[uint64]*subscriber),
	}
}

func (b *Broker) Subscribe(sessionID string) (<-chan InteractionEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subs[sessionID] == nil {
		b.subs[sessionID] = make(map[uint64]*subscriber)
	}

	id := b.next
	b.next++

	sub := &subscriber{ch: make(chan InteractionEvent, 32)}
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

func (b *Broker) Publish(sessionID string, event InteractionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, sub := range b.subs[sessionID] {
		select {
		case sub.ch <- event:
		default:
		}
	}
}

func (b *Broker) PublishAndRemove(sessionID string, event InteractionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, sub := range b.subs[sessionID] {
		select {
		case sub.ch <- event:
		default:
		}
		close(sub.ch)
	}
	delete(b.subs, sessionID)
}
