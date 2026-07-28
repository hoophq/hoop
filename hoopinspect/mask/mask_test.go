package mask_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoopinspect/mask"
)

// newMasker fails the test rather than returning an error, so the table tests
// below stay about masking and not about construction.
func newMasker(t *testing.T, rules ...mask.Rule) *mask.Masker {
	t.Helper()
	m, err := mask.New(rules)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// 4111111111111111 is the standard Visa test number; 4111111111111112 is its
// neighbour with a deliberately broken check digit.
const (
	validCard   = "4111111111111111"
	invalidCard = "4111111111111112"
)

func TestLuhnGatesCreditCardDetection(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityCreditCard, Strategy: mask.StrategyRedact})

	tests := []struct {
		name   string
		in     string
		masked bool
	}{
		{"valid visa", validCard, true},
		{"invalid check digit", invalidCard, false},
		{"valid amex 15 digit", "378282246310005", true},
		{"valid mastercard hyphenated", "5555-5555-5555-4444", true},
		{"valid discover spaced", "6011 1111 1111 1117", true},

		// The reason Luhn is here at all. Each of these is a plausible
		// column in a real result set and none is a card number.
		{"millisecond timestamp", "1705315200123456", false},
		{"sequential order id", "1234567890123456", false},
		{"repeated digit id", "9999999999999999", false},

		// Boundaries: 12 digits is below the window, 20 above it.
		{"twelve digits", "411111111111", false},
		{"twenty digits", "41111111111111111111", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, res := m.MaskString("value " + tc.in + " end")
			got := res.Count > 0
			if got != tc.masked {
				t.Fatalf("masked=%v want %v (output %q)", got, tc.masked, out)
			}
			if !tc.masked && strings.Contains(out, "REDACTED") {
				t.Errorf("unmatched input was rewritten: %q", out)
			}
			if tc.masked && strings.Contains(out, tc.in) {
				t.Errorf("card survived masking: %q", out)
			}
		})
	}
}

// A digit run Luhn rejects must stay unclaimed, so a later rule can still
// mask it. Otherwise a validator failure would silently create a hole that a
// broader fallback rule could not cover.
func TestValidatorRejectionLeavesSpanAvailable(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntityCreditCard, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: "long_digits", Pattern: `\b\d{16}\b`, Strategy: mask.StrategyRedact},
	)

	out, res := m.MaskString("id " + invalidCard)
	if want := "id [REDACTED:long_digits]"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if len(res.Entities) != 1 || res.Entities[0] != "long_digits" {
		t.Errorf("entities = %v, want [long_digits]", res.Entities)
	}
}

func TestStrategyMaskPreservesLength(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyMask})

	const ssn = "123-45-6789"
	out, res := m.MaskString("a|" + ssn + "|b")

	if res.Count != 1 {
		t.Fatalf("count = %d, want 1", res.Count)
	}
	if len(out) != len("a|"+ssn+"|b") {
		t.Errorf("total length changed: %q (%d) vs %d", out, len(out), len("a|"+ssn+"|b"))
	}
	if want := "a|***********|b"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// Length preservation is in runes, not bytes: a fixed-width consumer counts
// columns. A multi-byte mask char over a multi-byte value must keep the rune
// count identical even though the byte count moves.
func TestStrategyMaskPreservesRuneCountNotBytes(t *testing.T) {
	m := newMasker(t, mask.Rule{
		Entity: "unicode_tag", Pattern: `caf\x{e9}-\d+`,
		Strategy: mask.StrategyMask, MaskChar: '█',
	})

	const value = "café-42" // 7 runes, 8 bytes
	out, res := m.MaskString(value)

	if res.Count != 1 {
		t.Fatalf("count = %d, want 1", res.Count)
	}
	if got := len([]rune(out)); got != 7 {
		t.Errorf("rune count = %d, want 7 (output %q)", got, out)
	}
	if out != strings.Repeat("█", 7) {
		t.Errorf("output = %q, want 7 block chars", out)
	}
}

func TestStrategyPartialKeepsExactlyKeepLast(t *testing.T) {
	tests := []struct {
		name     string
		keepLast int
		in       string
		want     string
	}{
		{"default keeps four", 0, validCard, "************1111"},
		{"explicit two", 2, validCard, "**************11"},
		// 4006111111111234 is Luhn-valid; the hyphens must survive so the
		// output still reads as a card number.
		{"separators preserved", 0, "4006-1111-1111-1234", "****-****-****-1234"},

		// KeepLast at or beyond the value length would pass the value
		// through in the clear, which is worse than useless because the
		// config says it is masked. It must fall back to a full mask.
		{"keep equals length", 16, validCard, "****************"},
		{"keep exceeds length", 99, validCard, "****************"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMasker(t, mask.Rule{
				Entity: mask.EntityCreditCard, Strategy: mask.StrategyPartial, KeepLast: tc.keepLast,
			})
			out, res := m.MaskString(tc.in)
			if res.Count != 1 {
				t.Fatalf("count = %d, want 1", res.Count)
			}
			if out != tc.want {
				t.Fatalf("output = %q, want %q", out, tc.want)
			}

			keep := tc.keepLast
			if keep == 0 {
				keep = mask.DefaultKeepLast
			}
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, tc.in)
			if keep >= len(digits) {
				keep = 0 // documented fallback: mask everything
			}
			// Separators are preserved everywhere, so the kept tail is
			// measured in alphanumerics: exactly KeepLast of them survive
			// after the last mask character, and they are the input's own.
			tail := out[strings.LastIndex(out, "*")+1:]
			kept := len(strings.Map(func(r rune) rune {
				if r == '-' || r == ' ' || r == '.' {
					return -1
				}
				return r
			}, tail))
			if kept != keep {
				t.Errorf("kept %d trailing chars, want %d (output %q)", kept, keep, out)
			}
			if keep > 0 && !strings.HasSuffix(out, tc.in[len(tc.in)-keep:]) {
				t.Errorf("kept tail is not the input's tail: %q vs %q", out, tc.in)
			}
		})
	}
}

func TestStrategyHashIsDeterministicAndCollisionFree(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyHash})

	first, _ := m.MaskString("alice@example.com")
	second, _ := m.MaskString("alice@example.com")
	if first != second {
		t.Fatalf("not deterministic across calls: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("output = %q, want a sha256: prefix", first)
	}
	if got := len(strings.TrimPrefix(first, "sha256:")); got != 16 {
		t.Errorf("digest length = %d, want 16 hex digits", got)
	}

	// The whole point of the strategy: a join key survives masking. Equal
	// values must collapse to one masked value across rows, and unequal
	// values must not.
	joined, res := m.MaskString("alice@example.com,bob@example.com,alice@example.com")
	if res.Count != 3 {
		t.Fatalf("count = %d, want 3", res.Count)
	}
	parts := strings.Split(joined, ",")
	if parts[0] != parts[2] {
		t.Errorf("equal inputs gave different outputs: %q vs %q", parts[0], parts[2])
	}
	if parts[0] == parts[1] {
		t.Errorf("different inputs gave equal outputs: %q", parts[0])
	}
	if strings.Contains(joined, "alice") || strings.Contains(joined, "example.com") {
		t.Errorf("plaintext survived hashing: %q", joined)
	}
}

func TestRedactMarkerNamesTheEntity(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: "employee_id", Pattern: `EMP-\d{5}`}, // strategy defaults to redact
	)

	out, _ := m.MaskString("123-45-6789 / EMP-01234")
	if want := "[REDACTED:ssn] / [REDACTED:employee_id]"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestBuiltinDetectors(t *testing.T) {
	tests := []struct {
		entity  mask.Entity
		hits    []string
		misses  []string
		context string
	}{
		{
			entity: mask.EntityEmail,
			hits:   []string{"a@b.co", "first.last+tag@sub.example.co.uk", "USER@EXAMPLE.COM"},
			misses: []string{"not an email", "@example.com", "user@", "user@localhost"},
		},
		{
			entity: mask.EntitySSN,
			hits:   []string{"123-45-6789", "001-01-0001"},
			// No word boundaries and no hyphens means it is some other
			// number; masking 9-digit runs blindly eats every id column.
			misses: []string{"123456789", "1234-56-7890", "12-345-6789"},
		},
		{
			entity: mask.EntityPhone,
			hits: []string{
				"+14155552671", "415-555-2671", "(415) 555-2671",
				"(415)555-2671", "415.555.2671", "1-415-555-2671",
			},
			misses: []string{"555-2671", "+1", "12345"},
		},
		{
			entity: mask.EntityIPAddress,
			hits: []string{
				"10.0.0.1", "255.255.255.255", "2001:db8::1",
				"::1", "fe80::1", "::ffff:10.0.0.1",
			},
			misses: []string{"999.1.2.3", "256.1.1.1", "1.2.3", "12:30:45"},
		},
		{
			entity: mask.EntityAWSKey,
			hits:   []string{"AKIAIOSFODNN7EXAMPLE"},
			misses: []string{"AKIAIOSFODNN7", "akiaiosfodnn7example", "BKIAIOSFODNN7EXAMPLE"},
		},
		{
			entity: mask.EntityJWT,
			hits: []string{
				"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP",
				"eyJhbGciOiJub25lIn0.eyJzdWIiOiJhZG1pbiJ9.", // alg=none, worth catching
			},
			// A dotted hostname or file path is three segments too. Without
			// the header check this detector would redact half a log line.
			misses: []string{
				"foo.bar.baz", "a.b.c", "config.prod.json", "eyJhbGci.short.sig",
				// Starts with eyJ but the header holds characters that are
				// not base64url at all.
				"eyJ!!!!!.eyJzdWIiOiIxIn0.sig",
				// Header is in the base64url alphabet but its length is one
				// more than a multiple of four, so no base64 string can
				// decode to it.
				"eyJhbGciO.eyJzdWIiOiIxIn0.sig",
				// Decodes to JSON, but to an array rather than an object.
				"eyJhIiwiYiJd.eyJzdWIiOiIxIn0.sig",
				// Decodes to a JSON object with no "alg" claim, so it is
				// some other base64url blob and not a token.
				"eyJmb28iOiJiYXIifQ.eyJzdWIiOiIxIn0.sig",
			},
		},
		{
			entity: mask.EntityPrivateKey,
			hits: []string{
				"-----BEGIN RSA PRIVATE KEY-----\nMIIEow==\n-----END RSA PRIVATE KEY-----",
				"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blb\n-----END OPENSSH PRIVATE KEY-----",
				"-----BEGIN PRIVATE KEY-----\ntruncated by a size limit",
			},
			misses: []string{"-----BEGIN CERTIFICATE-----", "-----BEGIN PUBLIC KEY-----"},
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.entity), func(t *testing.T) {
			m := newMasker(t, mask.Rule{Entity: tc.entity, Strategy: mask.StrategyRedact})
			marker := "[REDACTED:" + string(tc.entity) + "]"

			for _, in := range tc.hits {
				out, res := m.MaskString(in)
				if res.Count != 1 {
					t.Errorf("hit %q: count = %d, want 1 (output %q)", in, res.Count, out)
					continue
				}
				// A partial match leaves a readable remnant, which is the
				// failure mode that actually leaks data.
				if out != marker {
					t.Errorf("hit %q: output = %q, want the whole value replaced by %q", in, out, marker)
				}
			}
			for _, in := range tc.misses {
				out, res := m.MaskString(in)
				if res.Count != 0 {
					t.Errorf("miss %q: falsely masked as %q", in, out)
				}
			}
		})
	}
}

func TestMaskJSONMasksValuesNotKeys(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
	)

	// The key here is itself an email address. Masking it would break the
	// caller's lookup and hide nothing that the value does not already
	// expose, so it must survive verbatim.
	in := []byte(`{
	  "alice@example.com": {"ssn": "123-45-6789", "contact": "alice@example.com"},
	  "rows": [{"email": "bob@example.com"}, {"note": "no pii here"}],
	  "count": 2
	}`)

	out, res, err := m.MaskJSON(in)
	if err != nil {
		t.Fatalf("MaskJSON: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, out)
	}

	if _, ok := doc["alice@example.com"]; !ok {
		t.Errorf("sensitive-looking KEY was rewritten; keys present: %v", keysOf(doc))
	}
	nested, _ := doc["alice@example.com"].(map[string]any)
	if got := nested["ssn"]; got != "[REDACTED:ssn]" {
		t.Errorf("nested ssn value = %v, want redacted", got)
	}
	if got := nested["contact"]; got != "[REDACTED:email]" {
		t.Errorf("nested email value = %v, want redacted", got)
	}
	if _, ok := nested["ssn"]; !ok {
		t.Error(`the "ssn" key itself was rewritten`)
	}

	rows, _ := doc["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2 elements", rows)
	}
	if got := rows[0].(map[string]any)["email"]; got != "[REDACTED:email]" {
		t.Errorf("array element email = %v, want redacted", got)
	}
	if got := rows[1].(map[string]any)["note"]; got != "no pii here" {
		t.Errorf("clean value was altered: %v", got)
	}

	if res.Count != 3 {
		t.Errorf("count = %d, want 3 (two emails in values, one ssn)", res.Count)
	}
	if strings.Contains(string(out), "bob@example.com") || strings.Contains(string(out), "123-45-6789") {
		t.Errorf("a masked value survived in the output: %s", out)
	}
}

// A number must round-trip exactly. encoding/json without UseNumber routes
// every number through float64, which turns a 16-digit value into 4.111e+15 —
// corrupting precisely the data this package is meant to protect.
func TestMaskJSONPreservesNumericPrecision(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact})

	in := []byte(`{"account":9007199254740993,"ratio":0.1,"big":12345678901234567890}`)
	out, res, err := m.MaskJSON(in)
	if err != nil {
		t.Fatalf("MaskJSON: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("count = %d, want 0", res.Count)
	}
	for _, want := range []string{"9007199254740993", "0.1", "12345678901234567890"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("number %s was mangled: %s", want, out)
		}
	}
}

func TestMaskJSONInvalidInputReturnsInputAndError(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact})

	tests := []struct {
		name string
		in   string
	}{
		{"truncated object", `{"email": "alice@example.com"`},
		{"not json at all", "alice@example.com\tbob@example.com\n"},
		{"empty", ""},
		// Re-marshalling would keep the first value and silently drop the
		// rest, so a stream of concatenated documents must be refused.
		{"trailing data", `{"a":1} {"b":2}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			out, res, err := m.MaskJSON(in)
			if err == nil {
				t.Fatalf("expected an error, got output %q", out)
			}
			if string(out) != tc.in {
				t.Errorf("input was not returned unchanged: %q vs %q", out, tc.in)
			}
			if res.Count != 0 || res.Entities != nil {
				t.Errorf("result should be zero on error, got %+v", res)
			}

			// The documented fallback: the caller retries over raw bytes.
			if raw, rawRes := m.Mask(in); strings.Contains(tc.in, "@") && rawRes.Count == 0 {
				t.Errorf("Mask fallback found nothing in %q (output %q)", tc.in, raw)
			}
		})
	}
}

func TestMaskJSONMasksTopLevelScalarAndArray(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact})

	for _, tc := range []struct{ in, want string }{
		{`"alice@example.com"`, `"[REDACTED:email]"`},
		{`["a@b.co","plain"]`, `["[REDACTED:email]","plain"]`},
		{`null`, `null`},
	} {
		out, _, err := m.MaskJSON([]byte(tc.in))
		if err != nil {
			t.Fatalf("MaskJSON(%s): %v", tc.in, err)
		}
		if string(out) != tc.want {
			t.Errorf("MaskJSON(%s) = %s, want %s", tc.in, out, tc.want)
		}
	}
}

// Order is precedence. A value both rules can match is claimed by the first,
// masked exactly once, and never rewritten twice.
func TestOverlappingMatchesFirstRuleWins(t *testing.T) {
	const customFirst = "custom_card"
	custom := mask.Rule{Entity: customFirst, Pattern: `4111-?1111-?1111-?1111`, Strategy: mask.StrategyRedact}
	builtin := mask.Rule{Entity: mask.EntityCreditCard, Strategy: mask.StrategyPartial}

	t.Run("custom first", func(t *testing.T) {
		m := newMasker(t, custom, builtin)
		out, res := m.MaskString("card " + validCard)

		if want := "card [REDACTED:custom_card]"; out != want {
			t.Fatalf("output = %q, want %q", out, want)
		}
		if res.Count != 1 {
			t.Errorf("count = %d, want 1 (double-masked)", res.Count)
		}
		if len(res.Entities) != 1 || res.Entities[0] != customFirst {
			t.Errorf("entities = %v, want [%s]", res.Entities, customFirst)
		}
	})

	// Swapping the order swaps the winner. If it did not, precedence would
	// be an accident of the built-in table rather than of the config.
	t.Run("builtin first", func(t *testing.T) {
		m := newMasker(t, builtin, custom)
		out, res := m.MaskString("card " + validCard)

		if want := "card ************1111"; out != want {
			t.Fatalf("output = %q, want %q", out, want)
		}
		if res.Count != 1 {
			t.Errorf("count = %d, want 1", res.Count)
		}
		if len(res.Entities) != 1 || res.Entities[0] != string(mask.EntityCreditCard) {
			t.Errorf("entities = %v, want [credit_card]", res.Entities)
		}
	})
}

// A later rule whose match merely straddles a claimed span must be dropped
// whole, not trimmed: half a value rewritten by a second strategy is neither
// masked nor readable.
func TestPartiallyOverlappingMatchIsDroppedWhole(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: "inner", Pattern: `example\.com`, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
	)

	out, res := m.MaskString("alice@example.com")
	if want := "alice@[REDACTED:inner]"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if res.Count != 1 {
		t.Errorf("count = %d, want 1", res.Count)
	}
	if len(res.Entities) != 1 || res.Entities[0] != "inner" {
		t.Errorf("entities = %v, want [inner]", res.Entities)
	}
}

// The mirror of the case above: here the later rule's match STARTS inside a
// span an earlier rule already claimed. Overlap rejection has to look both
// backwards and forwards from the insertion point, and a check that only
// looks one way passes the test above while silently double-masking here.
func TestOverlapRejectedInBothDirections(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: "prefix", Pattern: `alice@example`, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: "suffix", Pattern: `example\.com`, Strategy: mask.StrategyRedact},
	)

	out, res := m.MaskString("alice@example.com")
	if want := "[REDACTED:prefix].com"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if res.Count != 1 {
		t.Errorf("count = %d, want 1", res.Count)
	}
	if len(res.Entities) != 1 || res.Entities[0] != "prefix" {
		t.Errorf("entities = %v, want [prefix]", res.Entities)
	}
}

// Adjacent spans do not overlap. A rule ending exactly where another begins
// must still apply, or an off-by-one in the overlap check would drop it.
func TestAdjacentSpansBothApply(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: "second", Pattern: `BBB`, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: "first", Pattern: `AAA`, Strategy: mask.StrategyRedact},
	)

	out, res := m.MaskString("AAABBB")
	if want := "[REDACTED:first][REDACTED:second]"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if res.Count != 2 {
		t.Errorf("count = %d, want 2", res.Count)
	}
	// Entities follow rule order, so the rule declared first is named first
	// even though its match appears second in the text.
	if len(res.Entities) != 2 || res.Entities[0] != "second" || res.Entities[1] != "first" {
		t.Errorf("entities = %v, want [second first]", res.Entities)
	}
}

// Non-overlapping matches from several rules all apply, and Entities is
// reported in rule order so a log query can rely on it being stable.
func TestResultNamesEntitiesInRuleOrderWithoutValues(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyHash},
		mask.Rule{Entity: mask.EntityAWSKey, Strategy: mask.StrategyMask},
	)

	// Emails appear before the SSN in the text; Entities must still follow
	// rule order, not order of appearance.
	const (
		email1 = "alice@example.com"
		email2 = "bob@example.com"
		ssn    = "987-65-4321"
		awsKey = "AKIAIOSFODNN7EXAMPLE"
	)
	out, res := m.MaskString(email1 + " " + email2 + " " + ssn + " " + awsKey)

	if res.Count != 4 {
		t.Fatalf("count = %d, want 4", res.Count)
	}
	want := []string{"ssn", "email", "aws_key"}
	if len(res.Entities) != len(want) {
		t.Fatalf("entities = %v, want %v", res.Entities, want)
	}
	for i, w := range want {
		if res.Entities[i] != w {
			t.Fatalf("entities = %v, want %v", res.Entities, want)
		}
	}

	// The contract that matters: Result is safe to write to an audit log.
	// It must name classes and never carry the data it removed.
	joined := strings.Join(res.Entities, "\x00")
	for _, secret := range []string{email1, email2, ssn, awsKey, "alice", "bob", "987", "AKIA"} {
		if strings.Contains(joined, secret) {
			t.Errorf("Result leaked %q: %v", secret, res.Entities)
		}
	}
	for _, secret := range []string{email1, email2, ssn, awsKey} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaked %q: %s", secret, out)
		}
	}
}

// A rule that fires twice names its entity once. MaskedEntities is a set of
// classes; the multiplicity lives in MaskedCount.
func TestResultDeduplicatesEntityNames(t *testing.T) {
	m := newMasker(t, mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact})

	_, res := m.MaskString("a@b.co c@d.co e@f.co")
	if res.Count != 3 {
		t.Errorf("count = %d, want 3", res.Count)
	}
	if len(res.Entities) != 1 {
		t.Errorf("entities = %v, want one name", res.Entities)
	}
}

func TestCleanPayloadIsUntouched(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: mask.EntityCreditCard, Strategy: mask.StrategyRedact},
	)

	in := []byte("SELECT id, name FROM users WHERE created_at > '2024-01-15'")
	out, res := m.Mask(in)

	if string(out) != string(in) {
		t.Errorf("output = %q, want unchanged", out)
	}
	if res.Count != 0 || res.Entities != nil {
		t.Errorf("result = %+v, want zero", res)
	}
}

func TestEmptyRuleSetMasksNothing(t *testing.T) {
	m := newMasker(t)

	out, res := m.MaskString("alice@example.com " + validCard)
	if out != "alice@example.com "+validCard {
		t.Errorf("output = %q, want unchanged", out)
	}
	if res.Count != 0 {
		t.Errorf("count = %d, want 0", res.Count)
	}

	jsonOut, jsonRes, err := m.MaskJSON([]byte(`{"email":"alice@example.com"}`))
	if err != nil {
		t.Fatalf("MaskJSON: %v", err)
	}
	if jsonRes.Count != 0 {
		t.Errorf("count = %d, want 0", jsonRes.Count)
	}
	if string(jsonOut) != `{"email":"alice@example.com"}` {
		t.Errorf("output = %s, want unchanged", jsonOut)
	}
}

func TestNewRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		rule mask.Rule
		want string
	}{
		{"no entity", mask.Rule{Strategy: mask.StrategyRedact}, "no entity"},
		{"unknown entity", mask.Rule{Entity: "passport"}, "unknown entity"},
		{"unknown strategy", mask.Rule{Entity: mask.EntitySSN, Strategy: "shred"}, "unknown strategy"},
		{"bad pattern", mask.Rule{Entity: "custom", Pattern: `([a-z`}, "bad pattern"},
		{"empty-matching pattern", mask.Rule{Entity: "custom", Pattern: `\d*`}, "empty string"},
		{"negative keep_last", mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyPartial, KeepLast: -1}, "keep_last"},
		// A surrogate half is not a valid rune, and appending one would emit
		// U+FFFD instead of the configured character.
		{"invalid mask_char", mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyMask, MaskChar: 0xD800}, "mask_char"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := mask.New([]mask.Rule{tc.rule})
			if err == nil {
				t.Fatalf("New accepted an invalid rule, returned %v", m)
			}
			if m != nil {
				t.Errorf("New returned a non-nil Masker alongside an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// One error naming every problem. Reporting only the first turns a fleet
// rollout into one restart per typo.
func TestNewReportsAllProblemsAtOnce(t *testing.T) {
	_, err := mask.New([]mask.Rule{
		{Name: "good", Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
		{Name: "first-bad", Entity: "passport"},
		{Entity: mask.EntityEmail, Strategy: "shred"},
		{Name: "third-bad", Entity: "custom", Pattern: `([a-z`},
	})
	if err == nil {
		t.Fatal("New accepted three invalid rules")
	}

	msg := err.Error()
	for _, want := range []string{"first-bad", "rule[2]", "third-bad"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "good") {
		t.Errorf("error blames a valid rule: %s", msg)
	}
	// A rejected entity should tell the operator what is available.
	if !strings.Contains(msg, "credit_card") {
		t.Errorf("unknown-entity error does not list the known entities: %s", msg)
	}
}

// One Masker serves every connection in the process, so concurrent Mask,
// MaskString and MaskJSON calls must neither race nor interfere. Run under
// -race; the value assertions catch shared-buffer corruption that the race
// detector would not see.
func TestConcurrentUseIsSafe(t *testing.T) {
	m := newMasker(t,
		mask.Rule{Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
		mask.Rule{Entity: mask.EntityEmail, Strategy: mask.StrategyHash},
		mask.Rule{Entity: mask.EntityCreditCard, Strategy: mask.StrategyPartial},
	)

	const payload = `{"user":"alice@example.com","ssn":"123-45-6789","card":"4111111111111111"}`
	wantJSON, _, err := m.MaskJSON([]byte(payload))
	if err != nil {
		t.Fatalf("MaskJSON: %v", err)
	}
	wantRaw, wantRes := m.MaskString(payload)

	const goroutines, iterations = 16, 100
	var wg sync.WaitGroup
	errs := make(chan string, goroutines*3)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for range iterations {
				switch g % 3 {
				case 0:
					got, res := m.MaskString(payload)
					if got != wantRaw || res.Count != wantRes.Count {
						errs <- "MaskString diverged: " + got
						return
					}
				case 1:
					got, _, err := m.MaskJSON([]byte(payload))
					if err != nil || string(got) != string(wantJSON) {
						errs <- "MaskJSON diverged: " + string(got)
						return
					}
				default:
					// A distinct input per goroutine catches a Masker that
					// caches state between calls.
					in := "alice" + string(rune('a'+g)) + "@example.com"
					got, res := m.MaskString(in)
					if res.Count != 1 || strings.Contains(got, "@") {
						errs <- "unique-input mask diverged: " + got
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
