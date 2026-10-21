// Package liveevents broadcasts small change notices to the web app of one
// organization, so a page can react when something changes instead of
// polling. An event names what changed, never its content: the page reads
// the resource again through the API, with the access checks that route has.
//
// A replica delivers its own events in-process. Run also sends them to the
// other replicas through Postgres LISTEN/NOTIFY.
package liveevents

import (
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"go.uber.org/zap"
)

// Event types. A page subscribes to one by its name.
const (
	// SidecarCheckedIn is a sidecar handshake the plane answered. ID is the
	// sidecar's ID.
	SidecarCheckedIn = "sidecar.checked_in"
)

// Event is one change notice.
type Event struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// subscriberBuffer holds the events a slow reader has not taken yet. A full
// buffer drops the event: a notice is a hint to read again, and the next one
// carries the same hint.
const subscriberBuffer = 16

// Hub holds the subscribers of one gateway replica.
type Hub struct {
	replica string
	log     *zap.SugaredLogger

	mu     sync.Mutex
	subs   map[string]map[chan Event]struct{}
	outbox chan envelope

	dropping  atomic.Bool
	listening atomic.Bool
}

func NewHub() *Hub {
	replica := uuid.NewString()
	return &Hub{
		replica: replica,
		log:     log.With("replica", replica),
		subs:    map[string]map[chan Event]struct{}{},
	}
}

var defaultHub = NewHub()

// Publish sends ev to every subscriber of orgID. It never blocks.
func Publish(orgID string, ev Event) { defaultHub.Publish(orgID, ev) }

// Subscribe returns the events of orgID and the function that ends the
// subscription. Call it when the reader leaves; it closes the channel.
func Subscribe(orgID string) (<-chan Event, func()) { return defaultHub.Subscribe(orgID) }

func (h *Hub) Publish(orgID string, ev Event) {
	h.mu.Lock()
	h.deliverLocked(orgID, ev)
	outbox := h.outbox
	h.mu.Unlock()
	if outbox == nil {
		return
	}
	select {
	case outbox <- envelope{Replica: h.replica, OrgID: orgID, Event: &ev}:
	default:
		if !h.dropping.Swap(true) {
			h.log.Warnf("live events: the NOTIFY queue is full, other replicas miss events until it drains")
		}
	}
}

func (h *Hub) Subscribe(orgID string) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	h.mu.Lock()
	if h.subs[orgID] == nil {
		h.subs[orgID] = map[chan Event]struct{}{}
	}
	h.subs[orgID][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[orgID], ch)
			if len(h.subs[orgID]) == 0 {
				delete(h.subs, orgID)
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

func (h *Hub) deliver(orgID string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deliverLocked(orgID, ev)
}

func (h *Hub) deliverLocked(orgID string, ev Event) {
	for ch := range h.subs[orgID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
