package http

import (
	"strconv"
	"sync"
	"time"
)

const (
	// seenTTL is how long a remembered credential stays watched after the
	// upstream last accepted it. Every acceptance renews it, so a token in
	// use never lapses; an hour outlasts the gap between an agent's calls
	// within one task.
	seenTTL = time.Hour

	// seenMax bounds the credentials one lane remembers. Every request is
	// scanned for each of them, head and body, so the bound is a cost
	// ceiling; a lane serves a handful of tokens.
	seenMax = 64

	// seenMaxBytes is the longest value remembered. An Authorization value
	// is tens of bytes; a longer one is not a token a vault holds.
	seenMaxBytes = 4 << 10
)

// SeenCredentials remembers the Authorization credentials an upstream
// accepted, for every connection of one lane.
//
// # Why
//
// A request's own Authorization header tells the lane which token to keep
// out of that request's URL and body. A request that sends no
// Authorization at all, and the token in its URL, tells it nothing, and that
// is the request an agent prompted to leak the token would send. The token
// was very likely seen on an earlier request of the same lane: the content
// that prompted the leak came back through it. So the lane remembers.
//
// # What is remembered
//
// Only a credential whose request the upstream answered with a 2xx. A
// value the upstream refuses is not a token anyone holds, and remembering
// it would let a caller fill the memory with junk. When the memory is full
// a new credential is not remembered: evicting the oldest would let a
// caller push the real token out with junk an upstream answers 2xx anyway.
// The cost is a token rotated in during such a flood going unremembered
// until the junk expires; its own requests are still checked.
//
// The values are held in clear, because they are searched for as bytes.
// The sidecar forwards each of them in clear on every request anyway.
//
// # Lifetime
//
// One per lane, shared by the codecs its factory builds. A rule reload
// builds a new factory and so a new memory: until the upstream accepts a
// credential again, only each request's own credentials are watched.
type SeenCredentials struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	max   int
	until map[string]time.Time // value -> when it lapses
}

// NewSeenCredentials returns an empty memory with the package's bounds.
func NewSeenCredentials() *SeenCredentials {
	return newSeenCredentials(seenMax, seenTTL, time.Now)
}

func newSeenCredentials(limit int, ttl time.Duration, now func() time.Time) *SeenCredentials {
	return &SeenCredentials{now: now, ttl: ttl, max: limit, until: make(map[string]time.Time)}
}

// remember records that the upstream accepted v now.
func (s *SeenCredentials) remember(v string) {
	if len(v) < minSecretBytes || len(v) > seenMaxBytes || !tokenShaped(v) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if _, known := s.until[v]; !known {
		s.prune(now)
		if len(s.until) >= s.max {
			return // full: keep what is known; see the type's comment
		}
	}
	s.until[v] = now.Add(s.ttl)
}

// tokenShaped reports a value worth remembering: no whitespace, ';' or ',',
// and at least one letter and one digit.
//
// A remembered value is refused outside Authorization on every connection
// of the lane. A caller who sends a value ordinary requests also carry (a
// content type, a host name, a path) as a bearer to an endpoint that
// answers 2xx anyway would otherwise have the lane refuse those requests
// for everyone. Issued tokens mix letters and digits and hold none of those
// separators; the values ordinary requests repeat mostly do not. It narrows
// that attack, it does not end it: a path segment with a digit in it still
// passes.
func tokenShaped(v string) bool {
	letter, digit := false, false
	for _, c := range []byte(v) {
		switch {
		case c <= ' ' || c == ';' || c == ',' || c == 0x7f:
			return false
		case '0' <= c && c <= '9':
			digit = true
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
			letter = true
		}
	}
	return letter && digit
}

// snapshot returns the credentials still remembered.
func (s *SeenCredentials) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.now())
	out := make([]string, 0, len(s.until))
	for v := range s.until {
		out = append(out, v)
	}
	return out
}

func (s *SeenCredentials) prune(now time.Time) {
	for v, until := range s.until {
		if !now.Before(until) {
			delete(s.until, v)
		}
	}
}

// String names how many credentials are remembered, never which: the
// values are bearer tokens, and %v of the struct would print them.
func (s *SeenCredentials) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strconv.Itoa(len(s.until)) + " remembered credentials"
}
