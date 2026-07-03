package resolver

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/miekg/dns"
)

func query(t *testing.T, r *Resolver, name string, qtype uint16) *dns.Msg {
	t.Helper()
	in := new(dns.Msg)
	in.SetQuestion(dns.Fqdn(name), qtype)
	pkt, err := in.Pack()
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	resp, err := r.HandleUDP(pkt, netip.AddrPort{})
	if err != nil {
		t.Fatalf("HandleUDP: %v", err)
	}
	if resp == nil {
		t.Fatalf("nil response for %q", name)
	}
	out := new(dns.Msg)
	if err := out.Unpack(resp); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	return out
}

func TestAAAAForKnownName(t *testing.T) {
	alloc := addressing.New("seed")
	addr, _ := alloc.AddName("pg-prod")

	r := New(alloc, "hoop")
	resp := query(t, r, "pg-prod.hoop", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answer count = %d, want 1", len(resp.Answer))
	}
	aaaa, ok := resp.Answer[0].(*dns.AAAA)
	if !ok {
		t.Fatalf("not AAAA: %T", resp.Answer[0])
	}
	got, ok := netip.AddrFromSlice(aaaa.AAAA.To16())
	if !ok || got != addr {
		t.Fatalf("got %v want %v", got, addr)
	}
}

func TestAForKnownNameReturnsV4(t *testing.T) {
	alloc := addressing.New("seed")
	_, _ = alloc.AddName("pg-prod")
	r := New(alloc, "hoop")
	resp := query(t, r, "pg-prod.hoop", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	// Dual-stack: A queries for a known name now return the connection's
	// CGNAT IPv4 address (the macOS-friendly path), not an empty NODATA.
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 A answer, got %d", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer is %T, want *dns.A", resp.Answer[0])
	}
	wantV4, _ := alloc.LookupNameV4("pg-prod")
	if a.A.String() != wantV4.String() {
		t.Errorf("A = %s, want %s", a.A, wantV4)
	}
	// 100.64.0.0/10 CGNAT range.
	if !strings.HasPrefix(a.A.String(), "100.") {
		t.Errorf("A %s is not in the 100.64.0.0/10 CGNAT range", a.A)
	}
	// A positive answer carries no authority SOA.
	if len(resp.Ns) != 0 {
		t.Errorf("positive A answer should have empty authority section, got %d", len(resp.Ns))
	}
}

func TestUnknownNameNXDOMAIN(t *testing.T) {
	alloc := addressing.New("seed")
	r := New(alloc, "hoop")
	resp := query(t, r, "ghost.hoop", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode = %s, want NXDOMAIN", dns.RcodeToString[resp.Rcode])
	}
	// NXDOMAIN must also carry the SOA for negative caching (RFC 2308).
	assertSOA(t, resp, "hoop.")
}

// TestAAAAHasNoAuthoritySOA pins that a positive AAAA answer does NOT
// carry the negative-response SOA — the SOA belongs only in NODATA /
// NXDOMAIN replies, and a stray SOA on a positive answer would be
// malformed.
func TestAAAAHasNoAuthoritySOA(t *testing.T) {
	alloc := addressing.New("seed")
	_, _ = alloc.AddName("pg-prod")
	r := New(alloc, "hoop")
	resp := query(t, r, "pg-prod.hoop", dns.TypeAAAA)
	if len(resp.Ns) != 0 {
		t.Fatalf("positive AAAA answer should have empty authority section, got %d records", len(resp.Ns))
	}
}

// assertSOA checks the response carries exactly one SOA in the authority
// section, owned by the expected zone.
func assertSOA(t *testing.T, resp *dns.Msg, zone string) {
	t.Helper()
	var soas int
	for _, rr := range resp.Ns {
		soa, ok := rr.(*dns.SOA)
		if !ok {
			continue
		}
		soas++
		if soa.Hdr.Name != zone {
			t.Errorf("SOA owner = %q, want %q", soa.Hdr.Name, zone)
		}
	}
	if soas != 1 {
		t.Fatalf("expected exactly 1 SOA in authority section, got %d (Ns has %d records)", soas, len(resp.Ns))
	}
}

func TestQueryOutsideTLDRefused(t *testing.T) {
	alloc := addressing.New("seed")
	r := New(alloc, "hoop")
	resp := query(t, r, "example.com", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode = %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
}

func TestMultiLabelName(t *testing.T) {
	alloc := addressing.New("seed")
	// Connection name carries an internal dot.
	wantAddr, _ := alloc.AddName("pg.prod")
	r := New(alloc, "hoop")
	resp := query(t, r, "pg.prod.hoop", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answer = %d", len(resp.Answer))
	}
	aaaa := resp.Answer[0].(*dns.AAAA)
	got, _ := netip.AddrFromSlice(aaaa.AAAA.To16())
	if got != wantAddr {
		t.Fatalf("got %v want %v", got, wantAddr)
	}
}

func TestCaseInsensitive(t *testing.T) {
	alloc := addressing.New("seed")
	_, _ = alloc.AddName("pg-prod")
	r := New(alloc, "hoop")
	resp := query(t, r, "PG-PROD.Hoop", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
}

func TestCustomTLD(t *testing.T) {
	alloc := addressing.New("seed")
	_, _ = alloc.AddName("svc")
	r := New(alloc, "hoop.internal")
	resp := query(t, r, "svc.hoop.internal", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
}
