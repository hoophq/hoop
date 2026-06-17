//go:build darwin

package netstack

import "testing"

// TestParseRequestedUnit covers the requested-name → Sc_unit mapping for
// the macOS utun device. The mapping is 1-based (Sc_unit=N+1 yields
// utunN; 0 lets the kernel pick), which is easy to get backwards, so we
// pin it.
func TestParseRequestedUnit(t *testing.T) {
	cases := []struct {
		name    string
		want    uint32
		wantErr bool
	}{
		{"", 0, false},        // kernel picks
		{"utun0", 1, false},   // utun0 -> Sc_unit 1
		{"utun7", 8, false},   // utun7 -> Sc_unit 8
		{"utun15", 16, false}, // double digit
		{"tun0", 0, true},     // Linux-style name rejected
		{"utun", 0, true},     // no number
		{"utunX", 0, true},    // non-numeric
		{"utun-1", 0, true},   // negative
	}
	for _, c := range cases {
		got, err := parseRequestedUnit(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseRequestedUnit(%q) = %d, want error", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRequestedUnit(%q) unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseRequestedUnit(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestAFInet6Prefix asserts the constant 4-byte family header is the
// big-endian encoding of AF_INET6 (30 on macOS). utun rejects frames
// with the wrong family prefix, so a regression here would make every
// write silently dropped by the kernel.
func TestAFInet6Prefix(t *testing.T) {
	// AF_INET6 is 30 on darwin; big-endian over 4 bytes is 00 00 00 1e.
	want := [utunHeaderLen]byte{0x00, 0x00, 0x00, 0x1e}
	if afInet6Prefix != want {
		t.Errorf("afInet6Prefix = % x, want % x", afInet6Prefix, want)
	}
}
