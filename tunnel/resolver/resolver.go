// Package resolver implements the tunnel-local DNS server. It answers
// queries for `<name>.<tld>` from a static table populated by the connection
// list and returns NXDOMAIN for anything else under the tunnel TLD.
//
// The resolver does not perform recursion. Queries outside the tunnel TLD
// return REFUSED so the host DNS path can take over (this is the safety net
// — in practice, the OS-level DNS routing in RD-208 only sends *.hoop here).
//
// IPv6 / IPv4 behaviour (dual-stack — see ADR-0001):
//
//   - AAAA queries for known names: NOERROR with the allocated ULA address.
//   - A queries for known names:    NOERROR with the allocated CGNAT v4
//     address. Dual-stack is required because macOS getaddrinfo()
//     suppresses AAAA on hosts without global IPv6 (AI_ADDRCONFIG), so a
//     v6-only answer is invisible to real apps there.
//   - Any RR type for unknown name: NXDOMAIN (with SOA, per RFC 2308).
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
		resp.Ns = append(resp.Ns, r.soa()) // RFC 2308 negative response
		return resp
	}

	addr, ok := r.alloc.LookupName(connName)
	if !ok {
		resp.Rcode = dns.RcodeNameError // NXDOMAIN
		resp.Ns = append(resp.Ns, r.soa())
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
		// Dual-stack: answer A with the connection's IPv4 address. This
		// is the path macOS apps actually use — getaddrinfo() honours
		// AI_ADDRCONFIG and won't request/return AAAA on a host with no
		// global IPv6, so a v6-only answer makes `psql -h foo.hoop` fail
		// even though `dig`/`ping6` work. Handing out an A record
		// sidesteps that entirely. If the name somehow has no v4
		// allocation (should never happen — AddName allocates both
		// atomically), fall back to a NODATA+SOA so the client can still
		// use the AAAA.
		if v4, ok := r.alloc.LookupNameV4(connName); ok {
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    30,
				},
				A: v4.AsSlice(),
			})
		} else {
			resp.Ns = append(resp.Ns, r.soa())
		}
	default:
		// Unknown qtype on a known name: NODATA. RFC 2308 §2.2 wants an
		// SOA in the authority section for the negative answer.
		resp.Ns = append(resp.Ns, r.soa())
	}
	return resp
}

// soa returns the synthetic Start-of-Authority record for the tunnel
// zone. We are an authoritative server for "<tld>." with no real
// secondary/refresh semantics (the zone lives entirely in memory and
// changes only when the connection list does), so the timer fields are
// nominal. The record exists solely to make negative answers (NXDOMAIN
// and NODATA) RFC 2308-compliant, which some stub resolvers — notably
// macOS getaddrinfo — require before they will accept the answer.
//
// TTL / Minttl are short (30s) to match the AAAA TTL: a connection's
// address can change when the daemon re-allocates on reconnect, so we
// never want a negative answer cached longer than a positive one.
func (r *Resolver) soa() *dns.SOA {
	zone := r.tld + "."
	return &dns.SOA{
		Hdr: dns.RR_Header{
			Name:   zone,
			Rrtype: dns.TypeSOA,
			Class:  dns.ClassINET,
			Ttl:    30,
		},
		Ns:      "ns." + zone,         // nominal primary NS name
		Mbox:    "hostmaster." + zone, // nominal admin mailbox
		Serial:  1,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  30,
	}
}

func (r *Resolver) isUnderTLD(name string) bool {
	if name == r.tld {
		return true
	}
	return strings.HasSuffix(name, "."+r.tld)
}
