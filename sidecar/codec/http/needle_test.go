package http

import (
	"strings"
	"testing"
)

// A needle is found wherever the writes split it, and only once it is
// whole.
func TestNeedleScanFindsANeedleSplitAnywhere(t *testing.T) {
	needle := "tok-0123456789abcdef"
	stream := "prefix " + needle + " suffix"
	for i := 0; i <= len(stream); i++ {
		for j := i; j <= len(stream); j++ {
			s := newNeedleScan([][]byte{[]byte("another-needle-xyz"), []byte(needle)})
			hit := false
			for _, part := range []string{stream[:i], stream[i:j], stream[j:]} {
				hit = hit || s.write([]byte(part))
			}
			if !hit {
				t.Fatalf("missed the needle split at %d and %d", i, j)
			}
		}
	}
	s := newNeedleScan([][]byte{[]byte(needle)})
	for k := 0; k < len(needle)-1; k++ {
		if s.write([]byte{needle[k]}) {
			t.Fatalf("reported a hit after %d of %d bytes", k+1, len(needle))
		}
	}
	if !s.write([]byte{needle[len(needle)-1]}) {
		t.Fatal("missed the needle fed byte by byte")
	}
}

// reset ends a stream: half a needle before it and half after are two
// streams, not one needle.
func TestNeedleScanForgetsAcrossReset(t *testing.T) {
	needle := "tok-0123456789abcdef"
	s := newNeedleScan([][]byte{[]byte(needle)})
	s.write([]byte(needle[:8]))
	s.reset()
	if s.write([]byte(needle[8:])) {
		t.Fatal("matched across a reset")
	}
}

// The scan holds no more than one needle's length of what it was fed,
// whatever the body's size.
func TestNeedleScanKeepsOnlyATail(t *testing.T) {
	s := newNeedleScan([][]byte{[]byte("tok-0123456789abcdef")})
	s.write([]byte(strings.Repeat("x", 1<<20)))
	s.write([]byte("y"))
	if len(s.tail) != len("tok-0123456789abcdef")-1 {
		t.Fatalf("tail holds %d bytes", len(s.tail))
	}
	var none *needleScan
	if none.write([]byte("tok-0123456789abcdef")) {
		t.Fatal("a nil scan found something")
	}
}
