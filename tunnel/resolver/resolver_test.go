package resolver

import (
	"net/netip"
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

func TestAForKnownNameEmptyNOERROR(t *testing.T) {
	alloc := addressing.New("seed")
	_, _ = alloc.AddName("pg-prod")
	r := New(alloc, "hoop")
	resp := query(t, r, "pg-prod.hoop", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 0 {
		t.Fatalf("expected empty A answer, got %d", len(resp.Answer))
	}
}

func TestUnknownNameNXDOMAIN(t *testing.T) {
	alloc := addressing.New("seed")
	r := New(alloc, "hoop")
	resp := query(t, r, "ghost.hoop", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode = %s, want NXDOMAIN", dns.RcodeToString[resp.Rcode])
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
