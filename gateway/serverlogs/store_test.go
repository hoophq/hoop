package serverlogs

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/log"
)

func find(entries []Entry, message string) *Entry {
	for i := range entries {
		if entries[i].Message == message {
			return &entries[i]
		}
	}
	return nil
}

// TestSingleRingSourcesAndVisibility pins the store contract: gateway log
// output and agent batches land in the one shared ring, gateway entries are
// visible to every requester, and agent entries are org-scoped with an empty
// stored OrgID visible to all.
func TestSingleRingSourcesAndVisibility(t *testing.T) {
	marker := fmt.Sprintf("store-test-%d", time.Now().UnixNano())

	// Gateway source: the package-init observer wires the process logger
	// straight into the ring.
	log.Infof("gateway entry %s", marker)
	gwMsg := "gateway entry " + marker

	AppendAgentLogs("org-a", "agent-id-a", "agent-a", []log.TailEntry{
		{Timestamp: time.Now(), Level: "info", Message: "agent-a entry " + marker},
	})
	AppendAgentLogs("", "agent-id-x", "agent-x", []log.TailEntry{
		{Timestamp: time.Now(), Level: "warn", Message: "agent-x entry " + marker},
	})

	orgA := Snapshot("org-a")
	if e := find(orgA, gwMsg); e == nil || e.Source != SourceGateway {
		t.Fatalf("gateway entry missing or mis-sourced for org-a: %+v", e)
	}
	if e := find(orgA, "agent-a entry "+marker); e == nil || e.AgentName != "agent-a" || e.Source != SourceAgent {
		t.Fatalf("org-a agent entry missing or malformed: %+v", e)
	}
	if find(orgA, "agent-x entry "+marker) == nil {
		t.Fatal("empty-OrgID agent entry not visible to org-a")
	}

	orgB := Snapshot("org-b")
	if find(orgB, "agent-a entry "+marker) != nil {
		t.Fatal("org-a agent entry leaked to org-b")
	}
	if find(orgB, gwMsg) == nil {
		t.Fatal("gateway entry not visible to org-b")
	}
	if find(orgB, "agent-x entry "+marker) == nil {
		t.Fatal("empty-OrgID agent entry not visible to org-b")
	}

	// Since applies the same visibility filter and advances the cursor past
	// filtered-out entries.
	entries, seq := Since("org-b", 0)
	if seq == 0 {
		t.Fatal("Since returned zero cursor with a non-empty ring")
	}
	if find(entries, "agent-a entry "+marker) != nil {
		t.Fatal("Since leaked org-a agent entry to org-b")
	}
	if fresh, next := Since("org-b", seq); len(fresh) != 0 || next != seq {
		t.Fatalf("Since at latest cursor = %d entries, cursor %d; want 0, %d", len(fresh), next, seq)
	}
}
