package featureflag

import (
	"sort"
	"sync"

	"github.com/hoophq/hoop/common/log"
)

type Stability string

const (
	StabilityExperimental Stability = "experimental"
	StabilityBeta         Stability = "beta"
)

type Component string

const (
	ComponentGateway Component = "gateway"
	ComponentAgent   Component = "agent"
	ComponentClient  Component = "client"
)

type Flag struct {
	Name        string
	Description string
	Default     bool
	Stability   Stability
	Components  []Component
}

// catalog is the single source of truth for all known feature flags.
// A flag not registered here cannot be enabled, stored, or read.
var catalog = map[string]Flag{
	"experimental.log_exec_input": {
		Name:        "experimental.log_exec_input",
		Description: "Include the truncated exec input as a structured log attribute on the agent (for SIEM export). May log sensitive content.",
		Default:     true,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentAgent},
	},
	"experimental.rdp_pii_detection": {
		Name:        "experimental.rdp_pii_detection",
		Description: "Enable async RDP PII detection workers and per-session analysis enqueue. Requires Presidio analyzer URL and tesseract OCR.",
		Default:     true,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway},
	},
	"experimental.rdp_pii_guard": {
		Name:        "experimental.rdp_pii_guard",
		Description: "Block RDP frames until realtime PII analysis clears them and kill the session on detection (hold-and-release). Requires Presidio analyzer URL and tesseract OCR.",
		Default:     true,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway},
	},
	"experimental.rulepacks": {
		Name:        "experimental.rulepacks",
		Description: "Enable Rulepacks (attribute bundles): /rulepacks endpoints, rulepack_id on attributes, hide rulepack-owned attributes from feature lists.",
		Default:     true,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway},
	},
	"experimental.db_exec_driver": {
		Name:        "experimental.db_exec_driver",
		Description: "Run Postgres/MySQL/MSSQL/Oracle exec commands through in-process Go database drivers instead of spawning the vendor CLI (psql/mysql/sqlcmd/sqlplus). Eliminates client meta-command shell escapes (e.g. psql \\!, sqlplus HOST) and keeps the connection credential out of any user-reachable process.",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentAgent},
	},
	"beta.oracle_native": {
		Name:        "beta.oracle_native",
		Description: "Enable native Oracle (TNS) database access so clients like sqlplus/DBeaver connect through hoop's local proxy. When disabled, Oracle connections cannot open a native proxy session.",
		Default:     true,
		Stability:   StabilityBeta,
		Components:  []Component{ComponentGateway, ComponentAgent, ComponentClient},
	},
	"experimental.http_session_analyzer": {
		Name:        "experimental.http_session_analyzer",
		Description: "Run the AI Session Analyzer on individual requests made through native HTTP resources (httpproxy/kubernetes/claude-code). Each request is warned or blocked per its risk tier without dropping the session. For WebSocket sessions only the initial upgrade request is analyzed; bytes exchanged after the upgrade are not inspected.",
		Default:     true,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway},
	},
	"experimental.ssh_guardrails": {
		Name:        "experimental.ssh_guardrails",
		Description: "Enforce guardrails on native SSH connections: exec commands are validated against input rules before they run, and session-channel output (interactive shell/exec) is validated against output rules before it reaches the client. Port-forward (direct-tcpip) channels are not inspected. Interactive shell stdin is validated separately by experimental.ssh_input_guardrails. Requires a DLP provider (Presidio) to be configured.",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentAgent},
	},
	"experimental.ssh_input_guardrails": {
		Name:        "experimental.ssh_input_guardrails",
		Description: "Best-effort guardrails on interactive SSH shell stdin: each command line typed on a session shell is reconstructed and validated against input rules before the Enter that submits it is forwarded; a match blocks the command and ends the session. Reconstruction is approximate — shell history recall, tab-completion and cursor editing can bypass it — so treat it as advisory and pair it with experimental.ssh_guardrails (output rules) for defense in depth. Only interactive shells are inspected (not exec, sftp or port-forwards). Requires a DLP provider (Presidio) to be configured.",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentAgent},
	},
}

// All returns every registered flag, sorted by name.
func All() []Flag {
	flags := make([]Flag, 0, len(catalog))
	for _, f := range catalog {
		flags = append(flags, f)
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// Lookup returns the flag definition if it exists in the catalog.
func Lookup(name string) (Flag, bool) {
	f, ok := catalog[name]
	return f, ok
}

// --- per-org cache used by gateway (process-local) ---

var (
	cacheMu sync.RWMutex
	// orgID -> flagName -> enabled
	cache = map[string]map[string]bool{}
)

// Set updates the in-process cache for a single flag in one org.
func Set(orgID, name string, enabled bool) {
	if _, ok := catalog[name]; !ok {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache[orgID] == nil {
		cache[orgID] = map[string]bool{}
	}
	cache[orgID][name] = enabled
}

// SetAll replaces the entire flag snapshot for an org.
func SetAll(orgID string, flags map[string]bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	filtered := make(map[string]bool, len(flags))
	for name, enabled := range flags {
		if _, ok := catalog[name]; ok {
			filtered[name] = enabled
		}
	}
	cache[orgID] = filtered
}

// IsEnabled returns the effective value for a flag in an org.
// Unknown flags return false and log a warning.
func IsEnabled(orgID, name string) bool {
	f, ok := catalog[name]
	if !ok {
		log.Warnf("featureflag: unknown flag %q, returning false", name)
		return false
	}
	cacheMu.RLock()
	orgFlags, orgOK := cache[orgID]
	if orgOK {
		if val, found := orgFlags[name]; found {
			cacheMu.RUnlock()
			return val
		}
	}
	cacheMu.RUnlock()
	return f.Default
}

// SnapshotForOrg returns the effective boolean map for every catalog flag
// in the given org. Used to populate /serverinfo and gRPC packets.
func SnapshotForOrg(orgID string) map[string]bool {
	cacheMu.RLock()
	orgFlags := cache[orgID]
	cacheMu.RUnlock()

	snapshot := make(map[string]bool, len(catalog))
	for name, f := range catalog {
		if orgFlags != nil {
			if val, found := orgFlags[name]; found {
				snapshot[name] = val
				continue
			}
		}
		snapshot[name] = f.Default
	}
	return snapshot
}
