package http

import "strings"

// NormalizePath collapses dynamic path segments to "*", turning
// /users/12345/orders/98765 into /users/*/orders/*.
//
// # Rationale
//
// A policy keyed on the raw path needs one rule per id, which no one can
// write, so you end up with a regex per endpoint and get it wrong. Keying on
// the normalized resource gives one rule per endpoint:
//
//	deny if input.http.resource == "/users/*/ssn"
//
// # Dynamic segments
//
// A segment collapses when it is unambiguously an identifier rather than a
// route name:
//
//   - all digits            /users/12345
//   - a UUID                /users/3f6b...-...
//   - a hex string ≥ 12     /blobs/9f86d081884c7d65
//   - a long opaque token   /sessions/eyJhbGciOi... (≥ 24 chars, mixed case
//     or containing - _ . which are base64url/JWT shapes)
//
// A short alphanumeric slug like /users/alice is NOT collapsed. Nothing
// distinguishes it from a static route segment, and collapsing it would
// merge /users/alice with /users/settings, widening every rule written
// against either with no warning. In doubt this function keeps the segment,
// so a policy can end up too narrow but never too broad.
func NormalizePath(path string) string {
	if path == "" || path == "/" {
		return path
	}

	segs := strings.Split(path, "/")
	changed := false
	for i, s := range segs {
		if s == "" {
			continue
		}
		if norm, dynamic := normalizeSegment(s); dynamic {
			segs[i] = norm
			changed = true
		}
	}
	if !changed {
		return path
	}
	return strings.Join(segs, "/")
}

// normalizeSegment reports whether a segment is dynamic and, when it is,
// returns its collapsed form.
//
// A file extension is preserved: /reports/12345.pdf becomes /reports/*.pdf,
// not /reports/*. The extension carries part of the resource's identity (a
// policy may allow /exports/*.csv and deny /exports/*.sql), so collapsing it
// would merge the two into one rule.
func normalizeSegment(s string) (string, bool) {
	if dot := strings.LastIndexByte(s, '.'); dot > 0 && dot < len(s)-1 {
		ext := s[dot+1:]
		if isAlphaOnly(ext) && len(ext) <= 5 {
			if _, dynamic := normalizeSegment(s[:dot]); dynamic {
				return "*." + ext, true
			}
			return s, false
		}
	}
	if isDynamicSegment(s) {
		return "*", true
	}
	return s, false
}

// isDynamicSegment reports whether a bare segment (no file extension) is an
// identifier rather than a route name.
func isDynamicSegment(s string) bool {
	if isAllDigits(s) {
		return true
	}
	if isUUID(s) {
		return true
	}
	if len(s) >= 12 && isHex(s) {
		return true
	}
	if len(s) >= 24 && isOpaqueToken(s) {
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAlphaOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i] | 0x20 // lowercase
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		isHexAlpha := (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isDigit && !isHexAlpha {
			return false
		}
	}
	return true
}

// isUUID matches the canonical 8-4-4-4-12 hyphenated form.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, want := range []int{8, 13, 18, 23} {
		_ = i
		if s[want] != '-' {
			return false
		}
	}
	for i := range len(s) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		isHexAlpha := (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isDigit && !isHexAlpha {
			return false
		}
	}
	return true
}

// isOpaqueToken recognizes long base64url/JWT-shaped strings. They mix case
// or carry the base64url alphabet, which a human-authored route segment does
// not. The length floor keeps /users/settings out.
func isOpaqueToken(s string) bool {
	var hasUpper, hasLower, hasDigit, hasSep bool
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == '-' || c == '_' || c == '.' || c == '=' || c == '~':
			hasSep = true
		default:
			return false // not a token alphabet
		}
	}
	// Mixed case plus digits, or the base64url separators, is the signal.
	return (hasUpper && hasLower && hasDigit) || (hasSep && hasDigit)
}
