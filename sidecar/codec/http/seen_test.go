package http

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

const (
	tokA = "token-a-0123456789abcdef"
	tokB = "token-b-0123456789abcdef"
	tokC = "token-c-0123456789abcdef"
)

// A remembered credential lapses a TTL after the upstream last accepted
// it, and every acceptance renews it.
func TestSeenCredentialsLapseAfterTheirTTL(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	s := newSeenCredentials(4, time.Hour, clock.now)
	s.remember(tokA)
	clock.t = clock.t.Add(50 * time.Minute)
	s.remember(tokA)
	clock.t = clock.t.Add(50 * time.Minute)
	if got := s.snapshot(); !slices.Equal(got, []string{tokA}) {
		t.Fatalf("after a renewal = %v, want it remembered", got)
	}
	clock.t = clock.t.Add(10 * time.Minute)
	if got := s.snapshot(); len(got) != 0 {
		t.Fatalf("an hour after the last acceptance = %v, want it lapsed", got)
	}
}

// A full memory keeps what it knows: a caller who floods it with values an
// upstream accepts cannot push the real token out. A known value still
// renews, and a lapsed one makes room.
func TestAFullMemoryKeepsWhatItKnows(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	s := newSeenCredentials(2, time.Hour, clock.now)
	s.remember(tokA)
	clock.t = clock.t.Add(30 * time.Minute)
	s.remember(tokB)
	s.remember(tokC)
	got := s.snapshot()
	slices.Sort(got)
	if !slices.Equal(got, []string{tokA, tokB}) {
		t.Fatalf("full memory = %v, want the first two kept", got)
	}
	clock.t = clock.t.Add(20 * time.Minute)
	s.remember(tokA) // renews: tokA now lapses 20 minutes after tokB
	clock.t = clock.t.Add(45 * time.Minute)
	s.remember(tokC)
	got = s.snapshot()
	slices.Sort(got)
	if !slices.Equal(got, []string{tokA, tokC}) {
		t.Fatalf("after tokB lapsed = %v, want tokA and tokC", got)
	}
}

// A value too short to look for, or too long to be a token, is not kept.
func TestSeenCredentialsSkipValuesNotWorthWatching(t *testing.T) {
	s := NewSeenCredentials()
	s.remember("short-token")
	s.remember(strings.Repeat("x", seenMaxBytes+1))
	if got := s.snapshot(); len(got) != 0 {
		t.Fatalf("remembered %d values", len(got))
	}
}

// A value ordinary requests also carry is not remembered, or one caller
// could have the lane refuse those requests for everyone: a content type, a
// host name, a path without a digit.
func TestOnlyTokenShapedValuesAreRemembered(t *testing.T) {
	s := NewSeenCredentials()
	for _, v := range []string{
		"application/json; charset=utf-8",
		"kubernetes.default.svc",
		"api/namespaces/default",
		"0123456789012345678",
	} {
		s.remember(v)
	}
	if got := s.snapshot(); len(got) != 0 {
		t.Fatalf("remembered %q", got)
	}
	s.remember(tokA)
	if got := s.snapshot(); len(got) != 1 {
		t.Fatalf("a token was not remembered: %q", got)
	}
}

// The memory holds bearer tokens: printing it names a count only.
func TestSeenCredentialsNeverPrintAValue(t *testing.T) {
	s := NewSeenCredentials()
	s.remember(tokA)
	for _, verb := range []string{"%v", "%+v", "%s"} {
		if got := fmt.Sprintf(verb, s); strings.Contains(got, tokA) {
			t.Errorf("%s printed the token: %q", verb, got)
		}
	}
}
