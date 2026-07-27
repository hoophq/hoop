package tunnelmgr

import "sync"

// connRegistry is the concurrency-safe owner of the per-tunnel
// connection metadata: for each *.hoop name, its subtype and whether
// it is currently active (still present on the gateway).
//
// Why it exists: the netstack accept/handler closures and the
// /v1/connections Snapshot reader run on independent goroutines and
// read this metadata on every SYN / status poll. The periodic refresh
// (RD-209) writes it from yet another goroutine. A bare
// map[string]string shared by reference would be a data race; this type
// serialises all access behind a single RWMutex.
//
// Relationship to the Allocator: the Allocator owns name→IP and is
// append-only (IPs are deterministic from the session seed and never
// reassigned — see addressing/allocator.go). The registry layers the
// mutable "is this connection still offered by the gateway" state on
// top. When a connection is deleted on the gateway a refresh marks it
// inactive here (it drops out of listings and stops accepting new
// SYNs) while its IP stays reserved in the Allocator, so if it
// reappears it gets the same address.
type connRegistry struct {
	mu      sync.RWMutex
	entries map[string]connEntry
}

type connEntry struct {
	subType string
	// active is true while the gateway still offers this connection.
	// A refresh flips it to false for connections that vanished; the
	// accept path rejects new SYNs to inactive names and the listing
	// hides them.
	active bool
}

func newConnRegistry() *connRegistry {
	return &connRegistry{entries: make(map[string]connEntry)}
}

// subTypeOf returns the subtype for an ACTIVE connection. The second
// return is false for unknown names AND for known-but-inactive ones —
// callers (the netstack accept func) treat both as "do not serve".
func (r *connRegistry) subTypeOf(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok || !e.active {
		return "", false
	}
	return e.subType, true
}

// ConnInfo is a flattened, value-copied view of one active connection,
// safe to hand to callers outside the registry lock.
type ConnInfo struct {
	Name    string
	SubType string
}

// activeConnections returns a snapshot of the currently-active
// connections, copied out under the read lock so callers can range over
// the result without holding the lock or racing a refresh. Unordered;
// callers that need stable output sort by Name themselves.
func (r *connRegistry) activeConnections() []ConnInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ConnInfo, 0, len(r.entries))
	for name, e := range r.entries {
		if e.active {
			out = append(out, ConnInfo{Name: name, SubType: e.subType})
		}
	}
	return out
}

// reconcile sets the registry to exactly the supplied active set:
// upserts each (name, subType) as active and marks every other
// previously-known entry inactive. Entries are never deleted — an
// inactive entry stays so its IP reservation in the Allocator remains
// meaningful if the connection later reappears. Returns the number of
// names that became newly active (added or reactivated) and the number
// that went inactive, for logging.
func (r *connRegistry) reconcile(active map[string]string) (added, removed int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, subType := range active {
		prev, existed := r.entries[name]
		if !existed || !prev.active {
			added++
		}
		r.entries[name] = connEntry{subType: subType, active: true}
	}

	for name, e := range r.entries {
		if !e.active {
			continue
		}
		if _, stillActive := active[name]; !stillActive {
			r.entries[name] = connEntry{subType: e.subType, active: false}
			removed++
		}
	}
	return added, removed
}
