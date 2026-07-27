package broker

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// CreateAgent stores an entry with a nil *websocket.Conn; the capability
// helpers never touch the connection, so these tests exercise the capability
// logic and lifecycle without a live socket.
//
// AgentCapability waits up to AgentCapabilityWait for the capability frame.
// Tests that expect "unknown" must avoid paying that full wait where possible;
// they override nothing global, so they keep the default but assert via the
// pre-marked-ready paths or accept the bounded wait explicitly.

func TestAgentCapability_UnknownWhenUnregistered(t *testing.T) {
	value, known := AgentCapability("does-not-exist", CapabilitySupportsPIIGuard)
	if value || known {
		t.Fatalf("unregistered agent must be unknown+false, got value=%v known=%v", value, known)
	}
}

func TestAgentCapability_KnownIncapable(t *testing.T) {
	const name = "agent-incapable"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	SetAgentCapabilities(name, map[string]string{CapabilitySupportsPIIGuard: "false"})

	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	if !known {
		t.Fatalf("capabilities must be known after advertise")
	}
	if value {
		t.Fatalf("explicitly-incapable agent must report false")
	}
}

func TestAgentCapability_KnownCapable(t *testing.T) {
	const name = "agent-capable"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	SetAgentCapabilities(name, map[string]string{CapabilitySupportsPIIGuard: "true"})

	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	if !known {
		t.Fatalf("capabilities must be known after advertise")
	}
	if !value {
		t.Fatalf("capable agent must report true")
	}
}

// A missing key in an otherwise-known capability set is "known false", not
// "unknown": the agent advertised, it just doesn't have this capability.
func TestAgentCapability_KnownButKeyAbsent(t *testing.T) {
	const name = "agent-other-caps"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	SetAgentCapabilities(name, map[string]string{"some_other_capability": "true"})

	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	if !known {
		t.Fatalf("capabilities advertised, so must be known")
	}
	if value {
		t.Fatalf("absent key must report false")
	}
}

// SetAgentCapabilities on an unregistered agent must be a harmless no-op
// (the agent may have disconnected between connect and frame handling).
func TestSetAgentCapabilities_UnregisteredNoop(t *testing.T) {
	SetAgentCapabilities("ghost-agent", map[string]string{CapabilitySupportsPIIGuard: "true"})
	if _, known := AgentCapability("ghost-agent", CapabilitySupportsPIIGuard); known {
		t.Fatalf("setting caps on an unregistered agent must not create state")
	}
}

// A registered agent that never advertises must, after the bounded wait,
// report unknown so callers fail closed (old agent / degenerate connection).
func TestAgentCapability_UnknownAfterWaitWhenNeverAdvertised(t *testing.T) {
	const name = "agent-silent"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	// Shadow the wait via a fast deadline by measuring it stays bounded.
	start := time.Now()
	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	elapsed := time.Since(start)
	if known || value {
		t.Fatalf("silent agent must report unknown+false, got value=%v known=%v", value, known)
	}
	if elapsed < AgentCapabilityWait {
		t.Fatalf("must wait the full bound before giving up, waited %s", elapsed)
	}
}

// The connect race: a capability frame that arrives shortly AFTER a caller
// starts waiting must still be observed (not raced into a false "unknown").
func TestAgentCapability_ObservesLateAdvertisement(t *testing.T) {
	const name = "agent-late"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	go func() {
		time.Sleep(100 * time.Millisecond)
		SetAgentCapabilities(name, map[string]string{CapabilitySupportsPIIGuard: "true"})
	}()

	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	if !known || !value {
		t.Fatalf("late advertisement must be observed, got value=%v known=%v", value, known)
	}
}

// RemoveAgent must only delete the entry when the instance id matches: a stale
// connection closing late must not evict a newer connection that reused the
// same agent name.
func TestRemoveAgent_OnlyRemovesMatchingInstance(t *testing.T) {
	const name = "agent-reconnect"

	oldID, err := CreateAgent(name, nil)
	if err != nil {
		t.Fatalf("CreateAgent old: %v", err)
	}
	// A newer connection reuses the same name, replacing the entry.
	newID, err := CreateAgent(name, nil)
	if err != nil {
		t.Fatalf("CreateAgent new: %v", err)
	}
	if oldID == newID {
		t.Fatalf("instance ids must be unique")
	}
	defer BrokerInstance.agents.Delete(name)

	// The OLD connection closing must NOT remove the live (new) entry.
	RemoveAgent(name, oldID)
	if _, ok := getAgentEntry(name); !ok {
		t.Fatalf("stale connection removal evicted the live entry")
	}

	// The live connection closing removes it.
	RemoveAgent(name, newID)
	if _, ok := getAgentEntry(name); ok {
		t.Fatalf("live connection removal did not evict the entry")
	}
}

// Sentinel must stay byte-for-byte identical to the agent constant
// (ws::control::CONTROL_SENTINEL_SID) and pass header validation.
func TestControlSentinelSID_StableAndValid(t *testing.T) {
	if ControlSentinelSID == uuid.Nil {
		t.Fatal("sentinel must not be nil")
	}
	if ControlSentinelSID.Version() != 4 {
		t.Fatalf("sentinel must be version 4, got %d", ControlSentinelSID.Version())
	}
	const want = "00000000-0000-4c00-8000-000000000001"
	if got := ControlSentinelSID.String(); got != want {
		t.Fatalf("sentinel drifted from agent constant: got %s want %s", got, want)
	}
}

// Defensive copy: mutating the map passed to SetAgentCapabilities after it
// returns must not affect stored state.
func TestSetAgentCapabilities_DefensiveCopy(t *testing.T) {
	const name = "agent-mutate"
	if _, err := CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(name)

	m := map[string]string{CapabilitySupportsPIIGuard: "true"}
	SetAgentCapabilities(name, m)
	m[CapabilitySupportsPIIGuard] = "false" // mutate after store

	value, known := AgentCapability(name, CapabilitySupportsPIIGuard)
	if !known || !value {
		t.Fatalf("stored state must be insulated from caller mutation, got value=%v known=%v", value, known)
	}
}
