package main

import (
	"reflect"
	"testing"
)

func TestNormalizeToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "FooBar", "foobar"},
		{"strips separator punctuation", "Foo-Bar", "foobar"},
		{"strips surrounding whitespace and marks", "   !!! ", ""},
		{"keeps digits", "+1 512 989-1231", "15129891231"},
		{"strips dots but keeps the at sign", "a@b.com", "a@bcom"},
		{"empty stays empty", "", ""},
		{"non-ascii is dropped", "吃口", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeToken(tc.in); got != tc.want {
				t.Fatalf("normalizeToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The PII-shaped drop classifier keys on '@' (and digits). If normalizeToken
// stripped '@', an engine that lost it inside an email would normalize equal to
// the reference and the drop would never be reported — the exact leak the
// cross-check exists to catch. Lock the invariant for every marker rune.
func TestNormalizeTokenPreservesPIIMarkers(t *testing.T) {
	for _, r := range []rune{'@'} {
		if !piiMarkerRune(r) {
			t.Fatalf("piiMarkerRune(%q) = false, test data out of sync", r)
		}
		if got := normalizeToken(string(r)); got == "" {
			t.Fatalf("normalizeToken stripped PII marker %q; a candidate engine "+
				"losing it would compare equal and the drop would go unreported", r)
		}
	}
}

func TestDroppedTokens(t *testing.T) {
	cases := []struct {
		name string
		ref  []string
		got  []string
		want []string
	}{
		{
			name: "nothing dropped",
			ref:  []string{"alpha", "beta"},
			got:  []string{"beta", "alpha"},
			want: nil,
		},
		{
			name: "order and whitespace are not drops",
			ref:  []string{"Abc"},
			got:  []string{" a b c "},
			want: nil,
		},
		{
			name: "multiset: two refs one candidate drops one",
			ref:  []string{"a", "a"},
			got:  []string{"a"},
			want: []string{"a"},
		},
		{
			name: "missing token reported in original form",
			ref:  []string{"lucas@hoop.dev", "noise"},
			got:  []string{"noise"},
			want: []string{"lucas@hoop.dev"},
		},
		{
			name: "punctuation-only tokens ignored on both sides",
			ref:  []string{"|", "---"},
			got:  nil,
			want: nil,
		},
		{
			name: "email losing its at sign is a drop",
			ref:  []string{"a@b.com"},
			got:  []string{"ab.com"},
			want: []string{"a@b.com"},
		},
		{
			name: "candidate extras are not drops",
			ref:  []string{"alpha"},
			got:  []string{"alpha", "spurious"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := droppedTokens(tc.ref, tc.got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("droppedTokens(%q, %q) = %q, want %q", tc.ref, tc.got, got, tc.want)
			}
		})
	}
}
