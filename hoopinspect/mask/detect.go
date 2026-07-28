package mask

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"strings"
)

// Entity is a named class of sensitive data. The name is what ends up in
// Result.Entities and therefore in audit.Event.MaskedEntities, so it is part
// of the operator-facing contract: renaming one breaks the query someone
// wrote against last year's logs.
type Entity string

// Built-in entities. A Rule may also carry a custom Pattern with an
// operator-chosen Entity name, in which case none of these apply.
const (
	EntityEmail      Entity = "email"
	EntitySSN        Entity = "ssn"
	EntityCreditCard Entity = "credit_card"
	EntityPhone      Entity = "phone"
	EntityIPAddress  Entity = "ip_address"
	EntityAWSKey     Entity = "aws_key"
	EntityJWT        Entity = "jwt"
	EntityPrivateKey Entity = "private_key"
)

// detector finds candidate spans of one entity.
//
// The two-stage shape — a cheap RE2 prefilter plus an exact validator — is the
// whole point. RE2 has no backtracking and no lookaround, so it cannot express
// a checksum or a range check, and a regex written to be permissive enough to
// catch every real credit card also catches every order id. Splitting the job
// lets the regex be deliberately loose and the validator be exact.
//
// A candidate the validator rejects is not a match at all: its span stays
// unclaimed, so a later rule may still mask it.
type detector struct {
	re *regexp.Regexp

	// validate reports whether a regex hit is genuinely an instance of the
	// entity. nil means the regex is authoritative.
	validate func(string) bool
}

var builtins = map[Entity]detector{
	EntityEmail: {
		// Deliberately not RFC 5322. That grammar admits quoted local parts
		// and comments, and a masker that tries to honour it mostly finds
		// new ways to be wrong. This matches what appears in a result set.
		re: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,}\b`),
	},

	EntitySSN: {
		re: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	},

	EntityCreditCard: {
		// 13 to 19 digits, optionally grouped by a single space or hyphen.
		// The word boundaries matter as much as the length window: without
		// them a 25-digit run would yield a 19-digit prefix match.
		re:       regexp.MustCompile(`\b\d(?:[ \-]?\d){12,18}\b`),
		validate: luhn,
	},

	EntityPhone: {
		// Three shapes, because one alternation cannot cover them: E.164
		// (a literal + and 8-15 digits), the bare North American 3-3-4, and
		// the parenthesized area code. The paren form needs its own branch —
		// a leading \b cannot precede "(", since neither side of that
		// position is a word character, so folding it into the bare form
		// silently drops every "(415) 555-2671" in the payload.
		//
		// The trailing \b stops a 3-3-5 digit id from matching its own
		// prefix.
		re: regexp.MustCompile(`\+[1-9]\d{6,14}\b` +
			`|\b(?:\+?1[ \-.])?\d{3}[ \-.]\d{3}[ \-.]\d{4}\b` +
			`|(?:\+?1[ \-.]?)?\(\d{3}\)[ \-.]?\d{3}[ \-.]\d{4}\b`),
	},

	EntityIPAddress: {
		// Prefilter only; net.ParseIP decides. That is why the pattern can
		// afford to accept 999.1.2.3 and a timestamp-shaped colon run: both
		// are rejected a microsecond later, and hand-writing the full v6
		// grammar in RE2 is a reliable way to ship a masker with holes.
		//
		// Order is load-bearing. Go's regexp is leftmost-FIRST, so the
		// embedded-v4 form must come before the plain v6 form; reversed, the
		// greedy hex-group repetition claims "::ffff:10" and leaks the ".0.0.1"
		// tail of every IPv4-mapped address.
		re: regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){1,6}:\d{1,3}(?:\.\d{1,3}){3}` +
			`|[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}` +
			`|\b\d{1,3}(?:\.\d{1,3}){3}\b`),
		validate: validIP,
	},

	EntityAWSKey: {
		re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	},

	EntityJWT: {
		// Three base64url segments. A bare three-segment pattern also matches
		// every dotted hostname and file path, so the header must start with
		// "eyJ" — base64url of `{"`, which every JWT header begins with — and
		// the validator decodes it. The signature segment is allowed to be
		// empty because alg=none tokens are exactly the ones worth catching.
		re:       regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]*`),
		validate: validJWT,
	},

	EntityPrivateKey: {
		// The header alone is worthless to mask; the key material is the
		// body, so both alternatives have to consume it.
		//
		// The first takes a complete PEM block. The second is the fallback
		// for a block a response-size limit cut short, and it must still eat
		// the base64 that follows the header — matching the header alone
		// would emit "[REDACTED:private_key]" directly above the key
		// material it claimed to remove. It stops at the first character
		// that cannot appear in a PEM body rather than running to the end of
		// the payload, so a truncated key does not swallow unrelated rows.
		re: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----` +
			`|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[A-Za-z0-9+/=\r\n\t ]*`),
	},
}

// knownEntities is the sorted built-in list, used to make a config error
// actionable rather than just negative.
var knownEntities = func() []string {
	out := make([]string, 0, len(builtins))
	for e := range builtins {
		out = append(out, string(e))
	}
	sort.Strings(out)
	return out
}()

// luhn reports whether the digits in s satisfy the mod-10 checksum every
// payment card number carries, and whether there are between 13 and 19 of
// them.
//
// This is what keeps the credit-card detector usable. The regex it guards
// matches any 13-19 digit run, which in a real result set means order ids,
// millisecond timestamps, account numbers and phone numbers with a country
// code. Luhn is a single mod-10 check, so roughly one in ten arbitrary digit
// runs passes it by chance — a 90% cut in false positives for the cost of one
// pass over the string, and no false negatives at all, since every issued card
// number satisfies it by construction.
func luhn(s string) bool {
	sum, digits := 0, 0
	double := false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
		digits++
	}
	return digits >= 13 && digits <= 19 && sum%10 == 0
}

// validIP defers to the stdlib parser rather than trusting the prefilter.
func validIP(s string) bool { return net.ParseIP(s) != nil }

// validJWT decodes the header segment and requires it to be a JSON object
// naming an algorithm. Structure, not signature: this runs on a response body
// and has no key material, so the question is "is this a token" and not "is
// this token valid".
func validJWT(s string) bool {
	// The regex guarantees two dots, so Cut always splits; a dotless string
	// would simply fail the decode below.
	header, _, _ := strings.Cut(s, ".")

	// JWT segments are unpadded base64url, but be tolerant of a producer
	// that padded anyway.
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(header, "="))
	if err != nil {
		return false
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	_, hasAlg := claims["alg"]
	return hasAlg
}
