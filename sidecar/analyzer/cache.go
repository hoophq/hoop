package analyzer

import (
	"container/list"
	"sync"
	"time"
)

// cache is a bounded LRU of verdicts keyed on statement shape.
//
// It is the difference between a usable analyzer and an unusable one. An ORM
// issues one statement shape thousands of times in a session; without this,
// each one is a round trip and a charge. The key is the shape, so the hit
// rate on a real workload is high and the misses are the statements that are
// genuinely new.
//
// Caching a verdict cannot turn a block into an allow: the same shape always
// maps to the same verdict, and the entry expires on a TTL so a changed model
// or prompt takes effect within one window rather than at the next restart.
type cache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front is most recently used
	size    int
	ttl     time.Duration

	hits   uint64
	misses uint64

	// now is injected so tests can expire entries without sleeping.
	now func() time.Time
}

type cacheEntry struct {
	key      string
	result   Result
	memos    map[string]agentMemo
	expireAt time.Time
}

// agentMemo is an agent's decision kept with the classification it was made
// on: a hand-off decision, a rule decision (ENG-662), or both from one
// joined turn. The entry keys it by the agent's configuration and every
// input the agent read that the shape key does not cover.
type agentMemo struct {
	action Action
	reason string
	rules  *ruleDecision
}

// maxAgentMemos bounds the decisions one entry keeps, one per principal,
// database and so on. A full entry drops them all: the cost is new calls.
const maxAgentMemos = 64

// newCache returns a cache holding at most size entries for ttl each. A size
// or ttl of zero disables caching, and a disabled cache is still a valid
// object: every lookup misses and every store is a no-op, so the calling code
// has no branch.
func newCache(size int, ttl time.Duration) *cache {
	return &cache{
		entries: make(map[string]*list.Element, max(size, 0)),
		order:   list.New(),
		size:    size,
		ttl:     ttl,
		now:     time.Now,
	}
}

func (c *cache) enabled() bool { return c != nil && c.size > 0 && c.ttl > 0 }

// get returns a cached verdict for key.
func (c *cache) get(key string) (Result, bool) {
	if !c.enabled() {
		return Result{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[key]
	if !ok {
		c.misses++
		return Result{}, false
	}
	ent := el.Value.(*cacheEntry)
	if c.now().After(ent.expireAt) {
		// Expire on read rather than sweeping on a timer: an entry
		// nobody asks about costs one map slot until eviction, and a
		// background sweeper is a goroutine per lane for no gain.
		c.order.Remove(el)
		delete(c.entries, key)
		c.misses++
		return Result{}, false
	}
	c.order.MoveToFront(el)
	c.hits++
	return ent.result, true
}

// decision returns the agent decision the live entry for key keeps under
// memoKey. It does not count as a hit or a miss.
func (c *cache) decision(key, memoKey string) (agentMemo, bool) {
	if !c.enabled() {
		return agentMemo{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return agentMemo{}, false
	}
	ent := el.Value.(*cacheEntry)
	if c.now().After(ent.expireAt) {
		return agentMemo{}, false
	}
	m, ok := ent.memos[memoKey]
	return m, ok
}

// remember keeps an agent decision on the live entry for key, under
// memoKey. It does not extend the entry's TTL, and it does nothing when the
// entry is gone: the decision then is made again with the next
// classification.
func (c *cache) remember(key, memoKey string, m agentMemo) {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return
	}
	ent := el.Value.(*cacheEntry)
	if c.now().After(ent.expireAt) {
		return
	}
	if ent.memos == nil || len(ent.memos) >= maxAgentMemos {
		ent.memos = map[string]agentMemo{}
	}
	ent.memos[memoKey] = m
}

// put stores a verdict, evicting the least recently used entry when full.
func (c *cache) put(key string, res Result) {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[key]; ok {
		// A new classification: a decision made on the old one is stale.
		ent := el.Value.(*cacheEntry)
		ent.result = res
		ent.memos = nil
		ent.expireAt = c.now().Add(c.ttl)
		c.order.MoveToFront(el)
		return
	}

	el := c.order.PushFront(&cacheEntry{
		key:      key,
		result:   res,
		expireAt: c.now().Add(c.ttl),
	})
	c.entries[key] = el

	for c.order.Len() > c.size {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).key)
	}
}

// stats reports hit and miss counts. An operator reads these to decide
// whether the analyzer is affordable on a given lane before enforcing with
// it.
func (c *cache) stats() (hits, misses uint64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}
