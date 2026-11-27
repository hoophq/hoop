package analyzer

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
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

	// pinned counts the open prepared statements per key; eviction skips them.
	pinned map[string]int

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

	for el := c.order.Back(); el != nil && c.order.Len() > c.size; {
		prev := el.Prev()
		if ent := el.Value.(*cacheEntry); c.pinned[ent.key] == 0 {
			c.order.Remove(el)
			delete(c.entries, ent.key)
		}
		el = prev
	}
}

// pin keeps key's entry while a prepared statement with its text is open.
// Past maxPinned open texts, a new one is not kept.
func (c *cache) pin(key string) {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pinned == nil {
		c.pinned = map[string]int{}
	}
	if c.pinned[key] > 0 || len(c.pinned) < maxPinned {
		c.pinned[key]++
	}
}

func (c *cache) unpin(key string) {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pinned[key] > 1 {
		c.pinned[key]--
		return
	}
	delete(c.pinned, key)
}

const (
	preparedMemoSize = 256
	// The memo lives as long as its connection.
	preparedMemoTTL = 100 * 365 * 24 * time.Hour
	maxPinned       = 1024
)

// preparedMemo is the connection's memo of prepared statement verdicts, so
// the text makes one call however often it runs. Nil for other statements.
func preparedMemo(e *Evaluator, stmt inspect.Statement, ec *policy.EvalContext) *cache {
	switch stmt.Metadata[inspect.MetadataPrepared] {
	case inspect.PreparedPrepare, inspect.PreparedRun, inspect.PreparedRelease:
	default:
		return nil
	}
	if ec == nil || ec.Conn == nil {
		return nil
	}
	return ec.Conn.Value(e, func() any { return newCache(preparedMemoSize, preparedMemoTTL) }).(*cache)
}

// preparedKey keys the memo on what the prompt is built from: the database and
// the exact text. A close carries both, so it finds the key its prepare pinned.
func preparedKey(promptKey string, stmt inspect.Statement) string {
	sum := sha256.Sum256([]byte(stmt.Database + "\x00" + stmt.Text))
	return promptKey + ":" + hex.EncodeToString(sum[:])
}

// decision reads an agent decision from the connection's memo, then from
// the lane's cache.
func (e *Evaluator) decision(c classified, memoKey string) (agentMemo, bool) {
	if m, ok := c.memo.decision(c.textKey, memoKey); ok {
		return m, true
	}
	return e.cache.decision(c.key, memoKey)
}

func (e *Evaluator) remember(c classified, memoKey string, m agentMemo) {
	e.cache.remember(c.key, memoKey, m)
	c.memo.remember(c.textKey, memoKey, m)
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
