package memory

import "sync"

type ringItem[T any] struct {
	seq uint64
	val T
}

// Ring is a fixed-capacity, mutex-protected circular buffer that assigns a
// monotonically increasing sequence number to every appended entry. When full,
// appending evicts the oldest entry (tail semantics).
type Ring[T any] struct {
	mu   sync.RWMutex
	buf  []ringItem[T]
	next uint64 // next seq to assign, starts at 1
	head int    // index of oldest entry
	size int    // current count
}

// NewRing creates a ring buffer holding at most capacity entries.
// It panics when capacity <= 0.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		panic("memory: ring capacity must be > 0")
	}
	return &Ring[T]{buf: make([]ringItem[T], capacity), next: 1}
}

// Append stores v, assigning it the next sequence number and evicting the
// oldest entry when the ring is full.
func (r *Ring[T]) Append(v T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pos := (r.head + r.size) % len(r.buf)
	r.buf[pos] = ringItem[T]{seq: r.next, val: v}
	r.next++
	if r.size < len(r.buf) {
		r.size++
		return
	}
	r.head = (r.head + 1) % len(r.buf)
}

// Snapshot returns a chronological (oldest to newest) copy of the buffered entries.
func (r *Ring[T]) Snapshot() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	out := make([]T, 0, r.size)
	for i := range r.size {
		out = append(out, r.buf[(r.head+i)%len(r.buf)].val)
	}
	return out
}

// Since returns the buffered entries with sequence number greater than seq in
// chronological order, along with the latest sequence number observed (or seq
// itself when nothing newer is buffered). A seq older than the evicted window
// returns everything buffered.
func (r *Ring[T]) Since(seq uint64) ([]T, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil, seq
	}
	latest := r.next - 1
	if seq >= latest {
		return nil, seq
	}
	var out []T
	for i := range r.size {
		it := r.buf[(r.head+i)%len(r.buf)]
		if it.seq > seq {
			out = append(out, it.val)
		}
	}
	return out, latest
}

// LatestSeq returns the sequence number of the newest entry ever appended,
// or 0 when nothing has been appended yet.
func (r *Ring[T]) LatestSeq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.next - 1
}
