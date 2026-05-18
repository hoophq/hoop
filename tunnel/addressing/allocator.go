// Package addressing implements the deterministic name -> IPv6 ULA mapping
// used by `hsh tunnel`. See docs/adr/0001-tunnel-addressing.md for the design.
//
// An Allocator owns a single /48 ULA prefix derived from a session-stable
// seed. Every connection name hashes (SHA-256) into a stable address inside
// the prefix. Mappings are:
//
//   - Deterministic per session: same name + same seed -> same IP.
//   - Idempotent: AddName on a name that already exists is a no-op.
//   - Stable for the session lifetime: once added, a mapping cannot be
//     reassigned. RemoveName is intentionally absent — see the ADR.
//   - Indexed both ways: name <-> IP in O(1).
//
// Concurrency: safe for concurrent use. Adds are serialized; lookups take
// only a read lock.
package addressing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sync"
)

// PrefixLength is the per-session network mask (a /48 inside fd00::/8). 80
// bits per address gives us > 2^79 distinct mappings before birthday
// collisions become statistically interesting — i.e. never, in practice.
const PrefixLength = 48

// GatewayIP is the address of the netstack gateway inside every session's
// prefix. The DNS resolver binds here. The relative position of the address
// inside the /48 is fixed (`::1`) so the resolver is reachable from a
// well-known address derived purely from the prefix.
//
// Concretely, given a prefix `fd<48 bits of seed>::/48`, the gateway is
// `fd<48 bits of seed>:0:0:0:1`.
var gatewaySuffix = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

// Allocator owns the per-session prefix and the bidirectional name<->IP map.
type Allocator struct {
	prefix netip.Prefix // /48

	mu       sync.RWMutex
	byName   map[string]netip.Addr
	byAddr   map[netip.Addr]string
	gateway  netip.Addr
}

// New returns an Allocator whose /48 prefix is deterministically derived from
// seed. The seed should be the tunnel session identifier so a reconnect
// within the same session keeps the same prefix.
//
// The prefix is built as:
//
//	fd | sha256(seed)[0:5] | 0...
//
// (one byte ULA marker + 40 bits from the seed = 48 bits of prefix).
func New(seed string) *Allocator {
	h := sha256.Sum256([]byte(seed))
	var raw [16]byte
	raw[0] = 0xfd
	copy(raw[1:6], h[:5])
	addr := netip.AddrFrom16(raw)
	prefix := netip.PrefixFrom(addr, PrefixLength).Masked()

	a := &Allocator{
		prefix: prefix,
		byName: make(map[string]netip.Addr),
		byAddr: make(map[netip.Addr]string),
	}
	a.gateway = a.addrFromSuffix(gatewaySuffix)
	return a
}

// Prefix returns the per-session /48.
func (a *Allocator) Prefix() netip.Prefix { return a.prefix }

// Gateway returns the address the netstack uses as its default gateway and as
// the DNS resolver bind address.
func (a *Allocator) Gateway() netip.Addr { return a.gateway }

// AddName ensures name has an address allocated and returns it. Repeated
// calls with the same name return the same address. The first ever caller
// reserves the address; subsequent collisions on the suffix are resolved by
// linear probing.
//
// Collisions are vanishingly rare (80-bit suffix), but probing makes the
// behaviour correct rather than statistically-probably-correct.
func (a *Allocator) AddName(name string) (netip.Addr, error) {
	if name == "" {
		return netip.Addr{}, fmt.Errorf("addressing: empty name")
	}

	a.mu.RLock()
	if addr, ok := a.byName[name]; ok {
		a.mu.RUnlock()
		return addr, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	// Re-check under write lock.
	if addr, ok := a.byName[name]; ok {
		return addr, nil
	}

	suffix := suffixFromName(name)
	for attempt := 0; attempt < 16; attempt++ {
		// Reserved suffixes the netstack itself uses; never hand them to a
		// connection name.
		addr := a.addrFromSuffix(suffix)
		if addr == a.gateway {
			incrementSuffix(&suffix)
			continue
		}
		if existing, taken := a.byAddr[addr]; taken {
			// Collision with another name — keep probing. Should never happen
			// in practice but the loop is cheap.
			_ = existing
			incrementSuffix(&suffix)
			continue
		}
		a.byName[name] = addr
		a.byAddr[addr] = name
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("addressing: probe limit exhausted for %q", name)
}

// LookupName resolves a name to its allocated address. Returns false if the
// name has no allocation.
func (a *Allocator) LookupName(name string) (netip.Addr, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	addr, ok := a.byName[name]
	return addr, ok
}

// LookupAddr resolves an address back to its name. Returns false if the
// address has no allocation.
func (a *Allocator) LookupAddr(addr netip.Addr) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	name, ok := a.byAddr[addr]
	return name, ok
}

// Names returns a snapshot of all currently allocated names. Mostly useful
// for diagnostics; not for hot paths.
func (a *Allocator) Names() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.byName))
	for name := range a.byName {
		out = append(out, name)
	}
	return out
}

// addrFromSuffix combines the session prefix with a per-name suffix. The
// suffix is overlaid on the lower 80 bits of the address; the top 48 bits
// come from the prefix.
func (a *Allocator) addrFromSuffix(suffix [16]byte) netip.Addr {
	prefixBytes := a.prefix.Addr().As16()
	var out [16]byte
	// 48 bits = 6 bytes of prefix; the next 10 bytes are the suffix.
	copy(out[:6], prefixBytes[:6])
	copy(out[6:], suffix[6:])
	return netip.AddrFrom16(out)
}

// suffixFromName hashes name into a 10-byte suffix. The top 6 bytes of the
// returned [16]byte are zeros and ignored by addrFromSuffix.
func suffixFromName(name string) [16]byte {
	h := sha256.Sum256([]byte(name))
	var out [16]byte
	// Use bytes 0..9 of the hash for the suffix.
	copy(out[6:], h[:10])
	return out
}

// incrementSuffix bumps the suffix by one (big-endian, only the low 10
// bytes). Used to resolve probing collisions deterministically.
func incrementSuffix(s *[16]byte) {
	// Treat bytes 6..15 as a big-endian unsigned integer.
	low := binary.BigEndian.Uint64(s[8:])
	high := binary.BigEndian.Uint16(s[6:8])

	low++
	if low == 0 {
		high++
	}
	binary.BigEndian.PutUint16(s[6:8], high)
	binary.BigEndian.PutUint64(s[8:], low)
}
