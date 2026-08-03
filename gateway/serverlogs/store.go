// Package serverlogs keeps the single bounded in-memory tail of runtime log
// entries served by the server-logs endpoints: the gateway's own logs are
// appended through a log-capture observer registered at package init, and
// logs shipped by connected agents are appended by the transport layer. It
// is deliberately dependency-free within the gateway so both the transport
// and API layers can import it.
package serverlogs

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
)

// Source values for Entry.
const (
	SourceGateway = "gateway"
	SourceAgent   = "agent"
)

// ringCapacity bounds the server-logs tail shared by the gateway process and
// all connected agents.
const ringCapacity = 300

// Entry is one buffered log record. Gateway-sourced entries carry no org or
// agent identity; agent-sourced entries are annotated with the authenticated
// identity of the stream that shipped them.
type Entry struct {
	log.TailEntry
	Source    string
	OrgID     string
	AgentID   string
	AgentName string
}

var ring = memory.NewRing[Entry](ringCapacity)

// The gateway's own log output feeds the same ring as agent logs. Package
// init keeps this wiring boot-order free: any gateway binary that links the
// transport or API layer captures from the first post-init log call on.
func init() {
	log.TailObserve(func(e log.TailEntry) {
		ring.Append(Entry{TailEntry: e, Source: SourceGateway})
	})
}

// AppendAgentLogs annotates entries with the agent identity and appends them
// to the tail.
func AppendAgentLogs(orgID, agentID, agentName string, entries []log.TailEntry) {
	for _, e := range entries {
		ring.Append(Entry{
			TailEntry: e,
			Source:    SourceAgent,
			OrgID:     orgID,
			AgentID:   agentID,
			AgentName: agentName,
		})
	}
}

// visible reports whether an entry may be served to orgID. Gateway entries
// are process-wide and visible to any requester (the endpoints are
// admin-only); agent entries are org-scoped, with an empty stored OrgID
// (self-hosted, single-tenant) visible to all.
func visible(e Entry, orgID string) bool {
	return e.Source != SourceAgent || e.OrgID == "" || e.OrgID == orgID
}

// Snapshot returns the buffered entries visible to orgID in chronological
// order.
func Snapshot(orgID string) []Entry {
	entries := ring.Snapshot()
	out := entries[:0:0]
	for _, e := range entries {
		if visible(e, orgID) {
			out = append(out, e)
		}
	}
	return out
}

// Since returns the entries newer than seq visible to orgID, along with the
// latest sequence number observed. The returned cursor advances past
// filtered-out entries too.
func Since(orgID string, seq uint64) ([]Entry, uint64) {
	entries, latest := ring.Since(seq)
	var out []Entry
	for _, e := range entries {
		if visible(e, orgID) {
			out = append(out, e)
		}
	}
	return out, latest
}
