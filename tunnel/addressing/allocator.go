// Package addressing implements the deterministic name -> IP mapping used by
// `hsh tunnel`. See docs/adr/0001-tunnel-addressing.md for the design.
//
// An Allocator owns a single /48 ULA IPv6 prefix AND a per-session IPv4
// CGNAT (100.64.0.0/10) range, both derived from a session-stable seed.
// Every connection name hashes (SHA-256) into a stable address inside each
// family. Mappings are:
//
//   - Deterministic per session: same name + same seed -> same v6 & v4 IP.
//   - Idempotent: AddName on a name that already exists is a no-op.
//   - Stable for the session lifetime: once added, a mapping cannot be
//     reassigned. RemoveName is intentionally absent — see the ADR.
//   - Indexed both ways: name <-> v6 IP and name <-> v4 IP in O(1).
//
// # Why dual-stack
//
// The original design (ADR-0001) was IPv6-ULA only. That breaks on macOS:
// getaddrinfo() honours AI_ADDRCONFIG and refuses to query/return AAAA
// records when the host has no globally-routable IPv6 address — which is
// the common case (no v6 from Wi-Fi/Ethernet, only our tunnel). The
// documented workarounds (SystemConfiguration network service, broad v6
// routes) do not flip macOS 26's resolver out of "Request A records" mode.
// Apps therefore never see the AAAA and resolution fails, even though
// `dig`/`ping6` (which bypass AI_ADDRCONFIG) work.
//
// IPv4 has no such gating: macOS always queries A and always uses the
// answer. So every connection also gets a deterministic IPv4 address in
// the CGNAT range, the resolver answers A with it, and the netstack
// accepts v4 flows identically to v6. This sidesteps AI_ADDRCONFIG on
// every platform without per-OS branching.
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

// PrefixLength is the per-session IPv6 network mask (a /48 inside fd00::/8).
// 80 bits per address gives us > 2^79 distinct mappings before birthday
// collisions become statistically interesting — i.e. never, in practice.
const PrefixLength = 48

// v4 addressing constants. We carve a per-session /16 out of the CGNAT
// shared-address space 100.64.0.0/10 (RFC 6598). CGNAT is the right choice
// because, like ULA for v6, it is non-globally-routable and reserved for
// exactly this kind of intermediary use; it is also what Tailscale uses,
// so it is well-understood and unlikely to collide with a user's LAN
// (which almost always uses RFC 1918 10/8, 172.16/12, or 192.168/16).
//
// Layout of a per-session v4 address (32 bits):
//
//	100.64 . <seed byte> . <name byte>     ... is too small. Instead:
//	100 . 64+<seed:6bits> . <name:8> . <name:8>
//
// Concretely we fix the first octet at 100, take the /10 (100.64–100.127),
// derive the next 6 bits + a full octet from the seed to get a per-session
// /16-ish block, and hash the name into the low 16 bits. That yields ~65k
// addresses per session — far more than the handful of connections a user
// has, with linear probing for the rare collision.
const (
	v4FirstOctet = 100
	// v4CGNATSecondMin/Max bound the second octet to the 100.64.0.0/10
	// range (second octet 64..127).
	v4CGNATSecondMin = 64
	v4CGNATSecondMax = 127
)

// gatewaySuffix is the fixed suffix for the netstack gateway address (::1).
// gVisor owns this address; it is NOT assigned to the host TUN interface.
// The DNS resolver binds here inside gVisor. Given prefix `fd<X>::/48`,
// the gateway is `fd<X>::1`.
var gatewaySuffix = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

// hostSuffix is the fixed suffix for the host-side TUN interface address (::2).
// Linux owns this address; it is assigned to tun0 so the kernel has a valid
// source address when sending packets into the /48. It must differ from
// gatewaySuffix so traffic to the gateway goes through the TUN fd to gVisor
// rather than being consumed locally by the kernel.
var hostSuffix = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

// Allocator owns the per-session prefixes (v6 and v4) and the bidirectional
// name<->IP maps for both families.
type Allocator struct {
	prefix   netip.Prefix // IPv6 /48
	v4Prefix netip.Prefix // IPv4 /16 inside 100.64.0.0/10

	mu     sync.RWMutex
	byName map[string]netip.Addr // name -> IPv6
	byAddr map[netip.Addr]string // IPv6 -> name

	byNameV4 map[string]netip.Addr // name -> IPv4
	byAddrV4 map[netip.Addr]string // IPv4 -> name

	gateway   netip.Addr // ::1 inside v6 prefix — owned by gVisor
	host      netip.Addr // ::2 inside v6 prefix — assigned to host TUN interface
	gatewayV4 netip.Addr // x.x.0.1 — gVisor's v4 gateway / resolver bind
	hostV4    netip.Addr // x.x.0.2 — host TUN interface v4 address
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
		prefix:   prefix,
		v4Prefix: v4PrefixFromSeed(h),
		byName:   make(map[string]netip.Addr),
		byAddr:   make(map[netip.Addr]string),
		byNameV4: make(map[string]netip.Addr),
		byAddrV4: make(map[netip.Addr]string),
	}
	a.gateway = a.addrFromSuffix(gatewaySuffix)
	a.host = a.addrFromSuffix(hostSuffix)
	a.gatewayV4 = a.v4AddrFromSuffix(1) // x.x.0.1
	a.hostV4 = a.v4AddrFromSuffix(2)    // x.x.0.2
	return a
}

// v4PrefixFromSeed derives the per-session IPv4 /16 from the same seed hash
// used for the v6 prefix. The second octet is mapped into the CGNAT
// 100.64.0.0/10 window (64..127) and the third octet comes from the seed,
// giving each session a distinct /24-aligned block within a /16. Masked to
// /16 so the netstack can route the whole block locally.
func v4PrefixFromSeed(h [32]byte) netip.Prefix {
	second := v4CGNATSecondMin + int(h[5])%(v4CGNATSecondMax-v4CGNATSecondMin+1)
	third := h[6]
	base := netip.AddrFrom4([4]byte{v4FirstOctet, byte(second), third, 0})
	return netip.PrefixFrom(base, 16).Masked()
}

// Prefix returns the per-session IPv6 /48.
func (a *Allocator) Prefix() netip.Prefix { return a.prefix }

// PrefixV4 returns the per-session IPv4 /16 inside 100.64.0.0/10.
func (a *Allocator) PrefixV4() netip.Prefix { return a.v4Prefix }

// Gateway returns the address the netstack uses as its default gateway and as
// the DNS resolver bind address. This address is owned by gVisor — it is NOT
// assigned to the host TUN interface.
func (a *Allocator) Gateway() netip.Addr { return a.gateway }

// HostAddr returns the address that should be assigned to the host-side TUN
// interface. It differs from Gateway so that traffic to the gateway address
// is written into the TUN fd (and delivered to gVisor) rather than consumed
// locally by the kernel.
func (a *Allocator) HostAddr() netip.Addr { return a.host }

// GatewayV4 is the IPv4 counterpart of Gateway: gVisor's v4 address inside
// the netstack (x.x.0.1). The DNS resolver also answers on this address so
// a v4-only client can reach it. Owned by gVisor, not assigned to the host
// interface.
func (a *Allocator) GatewayV4() netip.Addr { return a.gatewayV4 }

// HostAddrV4 is the IPv4 counterpart of HostAddr: the address assigned to
// the host-side TUN interface (x.x.0.2) so the kernel has a valid v4 source
// address for traffic into the v4 range.
func (a *Allocator) HostAddrV4() netip.Addr { return a.hostV4 }

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

	// Allocate the IPv6 address.
	v6, err := a.allocV6Locked(name)
	if err != nil {
		return netip.Addr{}, err
	}
	// Allocate the parallel IPv4 address. If this fails we must not leave a
	// half-allocated name (v6 set, v4 missing), so roll back the v6 entry.
	v4, err := a.allocV4Locked(name)
	if err != nil {
		delete(a.byName, name)
		delete(a.byAddr, v6)
		return netip.Addr{}, err
	}
	_ = v4
	return v6, nil
}

// allocV6Locked reserves and records the IPv6 address for name. Caller must
// hold the write lock and have verified name is not already present.
func (a *Allocator) allocV6Locked(name string) (netip.Addr, error) {
	suffix := suffixFromName(name)
	for attempt := 0; attempt < 16; attempt++ {
		// Reserved addresses: gateway (gVisor) and host (TUN interface).
		// Never hand these to a connection name.
		addr := a.addrFromSuffix(suffix)
		if addr == a.gateway || addr == a.host {
			incrementSuffix(&suffix)
			continue
		}
		if _, taken := a.byAddr[addr]; taken {
			incrementSuffix(&suffix)
			continue
		}
		a.byName[name] = addr
		a.byAddr[addr] = name
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("addressing: v6 probe limit exhausted for %q", name)
}

// allocV4Locked reserves and records the IPv4 address for name. Caller must
// hold the write lock. The v4 space is far smaller than v6 (16 host bits),
// so collisions are more likely than in v6 but still rare for realistic
// connection counts; linear probing resolves them deterministically.
func (a *Allocator) allocV4Locked(name string) (netip.Addr, error) {
	low := v4SuffixFromName(name) // 16-bit host part
	for attempt := 0; attempt < 256; attempt++ {
		addr := a.v4AddrFromSuffix(low)
		if addr == a.gatewayV4 || addr == a.hostV4 {
			low++
			continue
		}
		if _, taken := a.byAddrV4[addr]; taken {
			low++
			continue
		}
		a.byNameV4[name] = addr
		a.byAddrV4[addr] = name
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("addressing: v4 probe limit exhausted for %q", name)
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
// address has no allocation. Accepts either family: an IPv4 address is
// looked up in the v4 table, anything else in the v6 table — so the
// netstack's accept/handler path can resolve a flow's destination without
// caring which family it arrived on.
func (a *Allocator) LookupAddr(addr netip.Addr) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if addr.Is4() {
		name, ok := a.byAddrV4[addr]
		return name, ok
	}
	name, ok := a.byAddr[addr]
	return name, ok
}

// LookupNameV4 resolves a name to its allocated IPv4 address. Returns false
// if the name has no allocation.
func (a *Allocator) LookupNameV4(name string) (netip.Addr, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	addr, ok := a.byNameV4[name]
	return addr, ok
}

// LookupAddrV4 resolves an IPv4 address back to its name. Returns false if
// the address has no allocation.
func (a *Allocator) LookupAddrV4(addr netip.Addr) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	name, ok := a.byAddrV4[addr]
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

// v4AddrFromSuffix combines the session /16 v4 prefix with a 16-bit host
// part. The top 16 bits come from the prefix; the low 16 bits are the
// supplied host value.
func (a *Allocator) v4AddrFromSuffix(low uint16) netip.Addr {
	p := a.v4Prefix.Addr().As4()
	p[2] = byte(low >> 8)
	p[3] = byte(low)
	return netip.AddrFrom4(p)
}

// v4SuffixFromName hashes name into a 16-bit host part for the v4 address.
// Uses a distinct slice of the hash from suffixFromName so a name's v4 and
// v6 low bits are independent (no structural correlation between them).
func v4SuffixFromName(name string) uint16 {
	h := sha256.Sum256([]byte(name))
	// Bytes 10..11 (just past the 10 bytes the v6 suffix consumes).
	return binary.BigEndian.Uint16(h[10:12])
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
