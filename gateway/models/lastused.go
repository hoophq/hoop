package models

import (
	"sync"
	"time"
)

const lastUsedInterval = time.Minute

type lastUsedGate struct {
	mu   sync.Mutex
	next map[string]time.Time
}

func (g *lastUsedGate) allow(id string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.next[id]) {
		return false
	}
	if g.next == nil {
		g.next = map[string]time.Time{}
	}
	g.next[id] = now.Add(lastUsedInterval)
	return true
}

var (
	apiKeyLastUsedGate  lastUsedGate
	aiAgentLastUsedGate lastUsedGate
)

// The gate holds one write per id and interval in this replica; the WHERE
// clause holds it across replicas.
func touchLastUsed(g *lastUsedGate, table, id string) {
	now := time.Now().UTC()
	if !g.allow(id, now) {
		return
	}
	DB.Table(table).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at <= ?)", id, now.Add(-lastUsedInterval)).
		Update("last_used_at", now)
}
