package models

import (
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
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

func (g *lastUsedGate) set(id string, next time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next[id] = next
}

var (
	apiKeyLastUsedGate  lastUsedGate
	aiAgentLastUsedGate lastUsedGate
)

// The gate holds one write per id and interval in this replica; the WHERE
// clause holds it across replicas.
func touchLastUsed(g *lastUsedGate, table, id string, now time.Time) {
	if !g.allow(id, now) {
		return
	}
	res := DB.Table(table).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at <= ?)", id, now.Add(-lastUsedInterval)).
		Update("last_used_at", now)
	if res.Error != nil {
		log.With("table", table, "id", id).Warnf("failed updating last_used_at, reason=%v", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		return
	}
	// Another replica wrote the row: open the gate when its interval ends.
	var row struct {
		LastUsedAt *time.Time `gorm:"column:last_used_at"`
	}
	err := DB.Table(table).Select("last_used_at").Where("id = ?", id).Scan(&row).Error
	if err != nil {
		log.With("table", table, "id", id).Warnf("failed reading last_used_at, reason=%v", err)
		return
	}
	if row.LastUsedAt != nil {
		g.set(id, row.LastUsedAt.Add(lastUsedInterval))
	}
}
