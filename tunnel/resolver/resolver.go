// Package resolver implements the tunnel-local DNS server. It answers
// queries for `<name>.<tld>` from a static table populated by the connection
// list and returns NXDOMAIN for anything else under the tunnel TLD.
//
// The resolver does not perform recursion. Queries outside the tunnel TLD
// return REFUSED so the host DNS path can take over (this is the safety net
// — in practice, the OS-level DNS routing in RD-208 only sends *.hoop here).
//
// IPv6 / IPv4 behaviour, per ADR-0001:
//
//   - AAAA queries for known names: NOERROR with the allocated ULA address.
//   - A queries for known names:    NOERROR with empty answer (we are v6-only).
//   - Any RR type for unknown name: NXDOMAIN.
package resolver

import (
	"net/netip"
	"strings"
	"sync"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/miekg/dns"
)

// DefaultTLD is the suffix the resolver answers for. The label is matched
// case-insensitively and without a trailing dot.
const DefaultTLD = "hoop"

// Resolver answers DNS queries from a name->address table. The table is
// backed by an addressing.Allocator; the resolver itself never adds names.
// Callers update the allocator separately when the connection list changes.
type Resolver struct {
	alloc *addressing.Allocator
	tld   string // lower-case, no leading or trailing dots

	mu sync.RWMutex
}

// New returns a Resolver that consults alloc for forward (name->IP) lookups
// and uses tld (case-insensitive, "hoop" by default) as the suffix it owns.
func New(alloc *addressing.Allocator, tld string) *Resolver {
	if tld == "" {
		tld = DefaultTLD
	}
	tld = strings.ToLower(strings.Trim(tld, "."))
	return &Resolver{alloc: alloc, tld: tld}
}

// TLD returns the resolver's configured suffix (no dots).
func (r *Resolver) TLD() string { return r.tld }

// HandleUDP is the entry point used by netstack.Stack via the DNSHandler
// option. It parses the wire-format DNS query, dispatches based on QTYPE,
// and returns the serialized response.
func (r *Resolver) HandleUDP(query []byte, src netip.AddrPort) ([]byte, error) {
	in := new(dns.Msg)
	if err := in.Unpack(query); err != nil {
		// Malformed query — drop it. Returning nil tells the netstack layer
		// to skip writing a reply.
		return nil, nil
	}
	resp := r.answer(in)
	if resp == nil {
		return nil, nil
	}
	return resp.Pack()
}

func (r *Resolver) answer(in *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(in)
	resp.Authoritative = true
	resp.RecursionAvailable = false

	if len(in.Question) != 1 {
		resp.Rcode = dns.RcodeFormatError
		return resp
	}
	q := in.Question[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))

	// Reject queries outside the tunnel TLD. Strictly speaking the OS
	// routing should prevent these from ever arriving; we double-check.
	if !r.isUnderTLD(name) {
		resp.Rcode = dns.RcodeRefused
		return resp
	}

	// Strip the TLD to find the connection name. We support both bare names
	// ("pg-prod.hoop") and multi-label names ("pg.prod.hoop"); the
	// allocator key is everything before the trailing ".<tld>".
	connName := strings.TrimSuffix(name, "."+r.tld)
	if connName == name || connName == "" {
		// Means name was exactly the TLD ("hoop.") — not a resource.
		resp.Rcode = dns.RcodeNameError
		return resp
	}

	addr, ok := r.alloc.LookupName(connName)
	if !ok {
		resp.Rcode = dns.RcodeNameError // NXDOMAIN
		return resp
	}

	switch q.Qtype {
	case dns.TypeAAAA:
		resp.Answer = append(resp.Answer, &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    30,
			},
			AAAA: addr.AsSlice(),
		})
	case dns.TypeA:
		// We are v6-only. Reply NOERROR with no records so callers fall back
		// to AAAA (RFC 8305 "happy eyeballs" already does this).
	default:
		// Unknown qtype on a known name: empty NOERROR is the polite reply.
	}
	return resp
}

func (r *Resolver) isUnderTLD(name string) bool {
	if name == r.tld {
		return true
	}
	return strings.HasSuffix(name, "."+r.tld)
}
