// Package serverlogs keeps a bounded in-memory tail of runtime log entries
// shipped by connected agents. It is deliberately dependency-free within the
// gateway so both the transport and API layers can import it.
package serverlogs

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
)

// agentRingCapacity bounds the agent log tail shared across all agents.
const agentRingCapacity = 5000

// AgentLogEntry is a log entry captured by an agent, annotated with the
// authenticated identity of the stream that shipped it.
type AgentLogEntry struct {
	log.TailEntry
	OrgID     string `json:"-"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
}

var agentRing = memory.NewRing[AgentLogEntry](agentRingCapacity)

// AppendAgentLogs annotates entries with the agent identity and appends them
// to the shared agent log tail.
func AppendAgentLogs(orgID, agentID, agentName string, entries []log.TailEntry) {
	for _, e := range entries {
		agentRing.Append(AgentLogEntry{
			TailEntry: e,
			OrgID:     orgID,
			AgentID:   agentID,
			AgentName: agentName,
		})
	}
}

// AgentSnapshot returns the buffered agent log entries visible to orgID in
// chronological order. Entries stored with an empty OrgID (self-hosted,
// single-tenant) are visible to all requesters.
func AgentSnapshot(orgID string) []AgentLogEntry {
	entries := agentRing.Snapshot()
	out := entries[:0:0]
	for _, e := range entries {
		if e.OrgID == "" || e.OrgID == orgID {
			out = append(out, e)
		}
	}
	return out
}

// AgentSince returns the entries newer than seq visible to orgID, along with
// the latest sequence number observed. The returned cursor advances past
// filtered-out entries too.
func AgentSince(orgID string, seq uint64) ([]AgentLogEntry, uint64) {
	entries, latest := agentRing.Since(seq)
	var out []AgentLogEntry
	for _, e := range entries {
		if e.OrgID == "" || e.OrgID == orgID {
			out = append(out, e)
		}
	}
	return out, latest
}
