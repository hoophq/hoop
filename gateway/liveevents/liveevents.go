// Package liveevents broadcasts small change notices to the web app of one
// organization, so a page can react when something changes instead of
// polling. An event names what changed, never its content: the page reads
// the resource again through the API, with the access checks that route has.
//
// It is in-process. A gateway with several replicas delivers an event only to
// the browsers connected to the replica that published it; pages keep their
// polling for that case.
package liveevents

import (
	"slices"
	"sync"
)

// Event types. A page subscribes to one by its name.
const (
	// SidecarCheckedIn is a sidecar's first handshake the plane answered. ID
	// is the sidecar's ID.
	SidecarCheckedIn = "sidecar.checked_in"
	// ReviewCreated is a review that waits for approval. ID is the review's ID.
	ReviewCreated = "review.created"
)

// Event is one change notice.
type Event struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	// Audience stays in the gateway: the stream sends only Type and ID.
	Audience *Audience `json:"-"`
}

// Audience is who may get an event: a member of one of Groups. OwnerID gets it
// only as an admin, as the requester of a review approves it only as one.
type Audience struct {
	Groups  []string
	OwnerID string
}

func (a *Audience) Allows(userID string, userGroups []string, isAdmin bool) bool {
	if a == nil {
		return false
	}
	if userID == a.OwnerID && !isAdmin {
		return false
	}
	for _, g := range userGroups {
		if slices.Contains(a.Groups, g) {
			return true
		}
	}
	return false
}

// subscriberBuffer holds the events a slow reader has not taken yet. A full
// buffer drops the event: a notice is a hint to read again, and the next one
// carries the same hint.
const subscriberBuffer = 16

var (
	mu   sync.Mutex
	subs = map[string]map[chan Event]struct{}{}
)

// Publish sends ev to every subscriber of orgID. It never blocks.
func Publish(orgID string, ev Event) {
	mu.Lock()
	defer mu.Unlock()
	for ch := range subs[orgID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns the events of orgID and the function that ends the
// subscription. Call it when the reader leaves; it closes the channel.
func Subscribe(orgID string) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	mu.Lock()
	if subs[orgID] == nil {
		subs[orgID] = map[chan Event]struct{}{}
	}
	subs[orgID][ch] = struct{}{}
	mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			mu.Lock()
			delete(subs[orgID], ch)
			if len(subs[orgID]) == 0 {
				delete(subs, orgID)
			}
			mu.Unlock()
			close(ch)
		})
	}
}
