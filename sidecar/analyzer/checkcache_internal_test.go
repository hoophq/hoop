package analyzer

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestTheCheckCacheEvictsTheOldestPastItsSize(t *testing.T) {
	c := newTTLCache[int](2, time.Minute)
	c.put("a", 1)
	c.put("b", 2)
	c.put("a", 3)
	c.put("c", 4)
	if _, ok := c.get("b"); ok {
		t.Fatal("b outlived the size cap; it was the oldest write")
	}
	if v, ok := c.get("a"); !ok || v != 3 {
		t.Fatalf("a = %d, %v", v, ok)
	}
	if v, ok := c.get("c"); !ok || v != 4 {
		t.Fatalf("c = %d, %v", v, ok)
	}
}

func TestThePlanKeyDoesNotGrowWithTheStatement(t *testing.T) {
	big := "SELECT * FROM t WHERE id IN (" + strings.Repeat("1,", 50_000) + "1)"
	key := planKey("db\x00role", big, nil, nil)
	if key == "" || len(key) > len("db\x00role")+1+sha256.Size {
		t.Fatalf("key is %d bytes for a %d-byte statement", len(key), len(big))
	}
	if planKey("db\x00role", big, nil, nil) != key {
		t.Fatal("the same statement got two keys")
	}
	if planKey("db\x00role", big+" ", nil, nil) == key {
		t.Fatal("two statements got one key")
	}
	if planKey("db\x00other", big, nil, nil) == key {
		t.Fatal("two scopes got one key")
	}
}

func TestTheCheckCacheExpiresAfterItsTTL(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTTLCache[int](10, 30*time.Second)
	c.now = func() time.Time { return now }
	c.put("a", 1)
	now = now.Add(30 * time.Second)
	if _, ok := c.get("a"); !ok {
		t.Fatal("expired at the TTL, not after it")
	}
	now = now.Add(time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Fatal("served past the TTL")
	}
	if len(c.entries) != 0 || c.order.Len() != 0 {
		t.Fatal("an expired entry stayed in the cache")
	}
}
