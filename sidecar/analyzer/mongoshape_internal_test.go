package analyzer

import (
	"strconv"
	"testing"
)

// The routing shape calls keyOf once per key, however many keys meet on one
// placeholder: a held statement can carry thousands of them.
func TestTheMongoDBRoutingShapeRewritesEachKeyOnce(t *testing.T) {
	const keys = 2000
	doc := make(map[string]any, keys)
	for i := range keys {
		doc[strconv.Itoa(i)] = true
	}
	calls := 0
	keyOf := func(key string) string {
		calls++
		return mongodbRoutingKey(key)
	}
	out := mongodbShapeValue(doc, "", keyOf).(map[string]any)
	if len(out) != keys {
		t.Fatalf("%d keys in the shape, want %d", len(out), keys)
	}
	if calls != keys {
		t.Errorf("keyOf ran %d times for %d keys", calls, keys)
	}
}
