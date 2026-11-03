package http

import (
	"bytes"
	"slices"
)

// minSecretBytes is the shortest credential the lane looks for outside its
// header. A shorter value is too likely to occur in an ordinary path or
// body ("Bearer test" against a body that says "test"), and a refusal on a
// coincidence is an outage. Issued tokens and the tokens a vault holds are
// far longer.
const minSecretBytes = 16

// needleScan finds any of a fixed set of values in a byte stream fed in
// arbitrary pieces.
//
// It keeps the last maxLen-1 bytes it was fed. That is shorter than any
// needle, so a needle split across two writes is found at the seam, and
// none can lie wholly inside the kept bytes to be found twice. Nothing else
// is held, so a body of any size costs at most that many bytes.
//
// A nil *needleScan finds nothing, so a request with no credential to look
// for pays nothing.
type needleScan struct {
	needles [][]byte
	maxLen  int
	tail    []byte // the last maxLen-1 bytes written since reset
	seam    []byte // scratch: tail and the head of the next write
}

// newNeedleScan returns a scanner for values, or nil when there are none.
// The caller drops short values and duplicates; see placementGuard.add. The
// list is copied, so the caller may reuse its own.
func newNeedleScan(values [][]byte) *needleScan {
	if len(values) == 0 {
		return nil
	}
	s := &needleScan{needles: slices.Clone(values)}
	for _, v := range values {
		s.maxLen = max(s.maxLen, len(v))
	}
	return s
}

// write feeds p, read right after everything written since the last reset,
// and reports whether a needle now appears.
func (s *needleScan) write(p []byte) bool {
	if s == nil || len(p) == 0 {
		return false
	}
	keep := s.maxLen - 1
	s.seam = append(append(s.seam[:0], s.tail...), p[:min(len(p), keep)]...)
	if s.contains(s.seam) || s.contains(p) {
		return true
	}
	s.tail = append(s.tail, p[max(0, len(p)-keep):]...)
	if over := len(s.tail) - keep; over > 0 {
		s.tail = s.tail[:copy(s.tail, s.tail[over:])]
	}
	return false
}

// contains reports whether b, read on its own, holds a needle.
func (s *needleScan) contains(b []byte) bool {
	return s != nil && containsAny(b, s.needles)
}

// reset starts a stream no needle may span from the previous one.
func (s *needleScan) reset() {
	if s != nil {
		s.tail = s.tail[:0]
	}
}

// containsAny reports whether b holds any of needles.
func containsAny(b []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(b, n) {
			return true
		}
	}
	return false
}
