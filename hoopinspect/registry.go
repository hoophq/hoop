package hoopinspect

import "sync"

// The registry lets codec packages plug themselves in without hoopinspect
// importing them, which keeps the root package dependency-free in both
// directions: `codec/postgres` imports `hoopinspect` for the types, and
// `hoopinspect` imports nothing.
//
// The practical payoff is binary size. An Envoy WASM filter that only speaks
// Postgres imports only `codec/postgres`, and the MongoDB BSON walker is never
// linked in.
var (
	registryMu sync.RWMutex
	registry   = map[Protocol]func() Codec{}
)

// Register makes a codec available to New.
//
// The argument is a FACTORY, not an instance, and that is load-bearing: the
// MSSQL and MySQL codecs reassemble messages that span packets, so they hold
// per-connection state. Handing every Inspector the same instance would let
// two connections corrupt each other's reassembly buffer — a data-dependent
// bug that surfaces as one tenant's SQL appearing in another's audit trail.
// A factory makes per-connection isolation the default rather than something
// every caller has to remember.
//
// It panics on a duplicate registration for the same protocol, because that
// can only be a build-time mistake (two codecs claiming one protocol) and
// silently picking a winner would make behavior depend on import order.
//
// Call it from a package init.
func Register(newCodec func() Codec) {
	if newCodec == nil {
		panic("hoopinspect: Register called with a nil factory")
	}
	probe := newCodec()
	if probe == nil {
		panic("hoopinspect: codec factory returned nil")
	}
	p := probe.Protocol()

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[p]; dup {
		panic("hoopinspect: duplicate codec registration for protocol " + string(p))
	}
	registry[p] = newCodec
}

func lookup(p Protocol) (func() Codec, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[p]
	return f, ok
}

// Registered lists the protocols with a codec linked into this binary, in no
// particular order. Useful for a startup log line that makes the build's
// actual capabilities visible.
func Registered() []Protocol {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Protocol, 0, len(registry))
	for p := range registry {
		out = append(out, p)
	}
	return out
}
