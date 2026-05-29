package addressing

import (
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func TestPrefixIsULA(t *testing.T) {
	a := New("session-1")
	p := a.Prefix()
	if p.Bits() != PrefixLength {
		t.Fatalf("prefix bits: got %d want %d", p.Bits(), PrefixLength)
	}
	if p.Addr().As16()[0] != 0xfd {
		t.Fatalf("prefix does not start with 0xfd: %v", p)
	}
}

func TestDeterministicAcrossInstances(t *testing.T) {
	a := New("session-xyz")
	b := New("session-xyz")
	addrA, _ := a.AddName("pg-prod")
	addrB, _ := b.AddName("pg-prod")
	if addrA != addrB {
		t.Fatalf("non-deterministic mapping: %v vs %v", addrA, addrB)
	}
}

func TestDifferentSessionsDifferentPrefixes(t *testing.T) {
	a := New("session-A")
	b := New("session-B")
	if a.Prefix() == b.Prefix() {
		t.Fatalf("different seeds produced identical prefixes: %v", a.Prefix())
	}
}

func TestIdempotentAdd(t *testing.T) {
	a := New("seed")
	addr1, err := a.AddName("svc")
	if err != nil {
		t.Fatal(err)
	}
	addr2, err := a.AddName("svc")
	if err != nil {
		t.Fatal(err)
	}
	if addr1 != addr2 {
		t.Fatalf("AddName not idempotent: %v vs %v", addr1, addr2)
	}
}

func TestBothDirections(t *testing.T) {
	a := New("seed")
	addr, _ := a.AddName("redis")
	got, ok := a.LookupAddr(addr)
	if !ok {
		t.Fatal("reverse lookup failed")
	}
	if got != "redis" {
		t.Fatalf("reverse lookup: got %q want %q", got, "redis")
	}
	got2, ok := a.LookupName("redis")
	if !ok || got2 != addr {
		t.Fatalf("forward lookup mismatch: %v / %v", got2, ok)
	}
}

func TestAddrInsidePrefix(t *testing.T) {
	a := New("seed")
	addr, _ := a.AddName("svc")
	if !a.Prefix().Contains(addr) {
		t.Fatalf("addr %v not inside prefix %v", addr, a.Prefix())
	}
}

func TestGatewayInsidePrefix(t *testing.T) {
	a := New("seed")
	gw := a.Gateway()
	if !a.Prefix().Contains(gw) {
		t.Fatalf("gateway %v not inside prefix %v", gw, a.Prefix())
	}
}

func TestGatewayNeverAllocatedToName(t *testing.T) {
	a := New("seed")
	// Seed many names; none should collide with the gateway.
	for i := 0; i < 1000; i++ {
		name := "conn-" + string(rune('a'+(i%26))) + string(rune('a'+((i/26)%26))) + string(rune('0'+(i%10)))
		addr, err := a.AddName(name)
		if err != nil {
			t.Fatal(err)
		}
		if addr == a.Gateway() {
			t.Fatalf("name %q allocated to gateway address", name)
		}
	}
}

func TestEmptyNameRejected(t *testing.T) {
	a := New("seed")
	_, err := a.AddName("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNamesSnapshot(t *testing.T) {
	a := New("seed")
	for _, n := range []string{"a", "b", "c"} {
		if _, err := a.AddName(n); err != nil {
			t.Fatal(err)
		}
	}
	got := a.Names()
	if len(got) != 3 {
		t.Fatalf("Names count: got %d want 3", len(got))
	}
}

func TestConcurrentAdds(t *testing.T) {
	a := New("seed")
	var wg sync.WaitGroup
	const N = 64
	results := make([]netip.Addr, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			addr, err := a.AddName("conn-shared")
			if err != nil {
				t.Errorf("AddName: %v", err)
				return
			}
			results[i] = addr
		}(i)
	}
	wg.Wait()
	// All goroutines must observe the same address.
	for i := 1; i < N; i++ {
		if results[i] != results[0] {
			t.Fatalf("race: %v vs %v", results[i], results[0])
		}
	}
}

func TestIncrementSuffix(t *testing.T) {
	var s [16]byte
	// Set the high suffix byte so a carry must propagate.
	s[15] = 0xff
	s[14] = 0xff
	s[13] = 0xff
	s[12] = 0xff
	s[11] = 0xff
	s[10] = 0xff
	s[9] = 0xff
	s[8] = 0xff
	s[7] = 0x00
	s[6] = 0x00
	incrementSuffix(&s)
	// Low 8 bytes wrap to zero, high 2 bytes bump to 0x0001.
	if s[15] != 0 || s[8] != 0 {
		t.Fatalf("low bytes did not wrap: % x", s)
	}
	if s[7] != 0x00 || s[6] != 0x00 {
		// Bytes 6/7 represent the upper 16 bits of the suffix.
		if s[7] != 0x01 {
			t.Fatalf("carry did not propagate: % x", s)
		}
	}
}

func TestNameWithDotsIsStillStable(t *testing.T) {
	// Tunnel users will pass dotted names ("pg-prod.hoop") through the
	// resolver; the allocator should not care.
	a := New("seed")
	a1, _ := a.AddName("pg-prod.hoop")
	a2, _ := a.AddName("pg-prod.hoop")
	if a1 != a2 || strings.Contains(a1.String(), "pg-prod") {
		t.Fatalf("name stability or unexpected encoding: %v / %v", a1, a2)
	}
}
