// Package mask rewrites sensitive values out of response payloads.
//
// # Why this is the interesting half
//
// Everything else in this library is request-side: read the statement, decide
// allow or deny. Envoy can do a narrow version of that for Postgres and
// nothing at all for the rest. But the request side is not where data leaves.
// `SELECT * FROM customers` is an entirely legitimate query for a support
// engineer to run, and the row it returns still contains a social security
// number they have no business reading.
//
// ext_authz cannot help here by construction: it is consulted BEFORE the
// upstream is called, so it has never seen the response. No Envoy filter masks
// database result sets, because no Envoy filter parses them. This package is
// the piece that has no equivalent anywhere in the proxy layer.
//
// # What it does
//
// A Masker holds an ordered list of Rules. Each Rule pairs an Entity (what to
// look for) with a Strategy (how to rewrite it). Mask scans a payload, claims
// the spans that match, and rewrites them in place:
//
//	m, err := mask.New([]mask.Rule{
//	    {Entity: mask.EntitySSN, Strategy: mask.StrategyRedact},
//	    {Entity: mask.EntityCreditCard, Strategy: mask.StrategyPartial, KeepLast: 4},
//	    {Entity: mask.EntityEmail, Strategy: mask.StrategyHash},
//	})
//	out, res := m.Mask(responseBytes)
//	if res.Count > 0 {
//	    sink.Write(ctx, audit.MaskedEvent(sess, res.Entities, res.Count))
//	}
//
// Result names WHAT was masked and how many times, never the values. That is
// not an oversight: an audit log recording the values it masked has un-masked
// them, and it is the log that gets shipped off-box to a search cluster.
//
// # What it is not
//
// Not a guarantee. Detection is pattern-based, so it is neither sound nor
// complete: a name column is not detectable, a base64 blob may hide anything,
// and a determined caller can chunk a value across two responses. Masking
// raises the cost of accidental exposure. It does not replace not granting
// access to the table.
//
// A Masker is immutable after New and safe for concurrent use by any number of
// connections.
package mask

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Strategy says how a matched span is rewritten.
type Strategy string

const (
	// StrategyRedact replaces the value with "[REDACTED:<entity>]". The
	// output tells the reader something was removed and what kind of thing
	// it was, which is what stops a support engineer filing a bug about
	// corrupted data.
	StrategyRedact Strategy = "redact"

	// StrategyMask replaces every character with MaskChar, preserving the
	// rune count. Use it when the consumer parses fixed-width columns and a
	// length change would shift every field after it.
	StrategyMask Strategy = "mask"

	// StrategyPartial keeps the last KeepLast characters and masks the
	// alphanumeric ones before them, leaving punctuation in place:
	// 4111-1111-1111-1234 becomes ****-****-****-1234. The tail is what a
	// human uses to confirm "yes, that is the card I meant", which is the
	// only reason to show any of it.
	StrategyPartial Strategy = "partial"

	// StrategyHash replaces the value with "sha256:<first 16 hex digits>".
	//
	// The point is that equal inputs give equal outputs, so a masked column
	// still works as a join key and a GROUP BY still counts distinct users.
	// The cost is real and worth stating plainly: the mapping is
	// deterministic and unsalted, so it LEAKS EQUALITY (you can tell two
	// rows share an email without learning it) and it is trivially reversed
	// by dictionary attack over any small value space. Hashing a US SSN is
	// pointless — there are only 10^9 of them and a laptop enumerates that
	// in minutes. Use it for high-entropy identifiers where correlation is
	// the requirement, and redact everything else.
	StrategyHash Strategy = "hash"
)

// DefaultKeepLast is the StrategyPartial tail length when KeepLast is unset.
// Four is the number printed on receipts, so it is the number people expect.
const DefaultKeepLast = 4

// DefaultMaskChar is the replacement character when MaskChar is unset.
const DefaultMaskChar = '*'

// Rule pairs a class of sensitive data with the rewrite applied to it.
//
// Rules are ordered and order is precedence: when two rules match overlapping
// text the earlier one claims the span and the later one does not see it. Put
// the specific rule first.
type Rule struct {
	// Name identifies the rule in configuration errors. Defaults to
	// "rule[<index>]".
	Name string `json:"name,omitempty"`

	// Entity selects a built-in detector, or names the custom Pattern for
	// Result.Entities. Required either way, because Result.Entities is what
	// an operator reads in the audit log.
	Entity Entity `json:"entity"`

	// Strategy is the rewrite. Empty means StrategyRedact: an unconfigured
	// rule should fail towards showing less, not more.
	Strategy Strategy `json:"strategy,omitempty"`

	// KeepLast is the StrategyPartial tail length. Zero means
	// DefaultKeepLast. A value at or above the length of a matched value
	// masks it entirely rather than passing it through untouched.
	KeepLast int `json:"keep_last,omitempty"`

	// MaskChar is the replacement rune for StrategyMask and StrategyPartial.
	// Zero means DefaultMaskChar.
	MaskChar rune `json:"mask_char,omitempty"`

	// Pattern is an optional RE2 expression that replaces the built-in
	// detector for Entity. Set it to mask a deployment-specific format — an
	// internal customer id, a bearer token with a known prefix — that no
	// general detector can know about.
	Pattern string `json:"pattern_regex,omitempty"`
}

// Result reports what a Mask call rewrote.
//
// It deliberately carries no values. This is the exact shape
// audit.MaskedEvent consumes.
type Result struct {
	// Entities names the distinct entity classes that were rewritten, in
	// rule order. Rule order rather than order of appearance so the field is
	// stable across payloads and a log query can rely on it.
	Entities []string `json:"entities,omitempty"`

	// Count is how many spans were rewritten.
	Count int `json:"count"`
}

// Masker applies an ordered rule set to a payload. Immutable after New and
// safe for concurrent use.
type Masker struct {
	rules []compiled
}

// compiled is a Rule with its defaults resolved and its regex built, so the
// data path never re-derives them and never touches the caller's Rule.
//
// Exactly one of re or detect finds candidate spans.
type compiled struct {
	entity   Entity
	strategy Strategy
	keepLast int
	maskChar rune
	re       *regexp.Regexp
	validate func(string) bool

	// detect is set when an external Detector owns this entity, in which
	// case re is nil and the Detector supplies the spans.
	detect func(data []byte) [][2]int

	// redaction is the StrategyRedact replacement, built once.
	redaction []byte
}

// New compiles a rule set.
//
// It reports EVERY invalid rule in one error rather than stopping at the
// first. A masking config is edited by hand and deployed to a fleet; finding
// out about the second typo on the next restart is how a rollout takes three
// rounds instead of one. And it fails at construction, not on the first
// request that trips the bad rule — a masker that silently passes a payload
// through because its regex never compiled is worse than one that refuses to
// start.
//
// An empty rule set is valid and masks nothing.
func New(rules []Rule) (*Masker, error) { return NewWithDetector(rules, nil) }

// NewWithDetector is New with an external Detector consulted for the entities
// it claims. A nil Detector is exactly New.
//
// Precedence is deliberate: the Detector wins for any entity it lists, even
// one with a built-in of the same name. Wiring a detector in is an explicit
// act, and a caller who does it and still silently gets the eight built-in
// regexes has been lied to. A rule carrying its own Pattern still overrides
// both — that is the operator's per-rule escape hatch.
func NewWithDetector(rules []Rule, d Detector) (*Masker, error) {
	detected := map[Entity]bool{}
	if d != nil {
		for _, e := range d.Entities() {
			detected[Entity(e)] = true
		}
	}
	var problems []string
	out := make([]compiled, 0, len(rules))

	for i, r := range rules {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("rule[%d]", i)
		}

		c := compiled{
			entity:   r.Entity,
			strategy: r.Strategy,
			keepLast: r.KeepLast,
			maskChar: r.MaskChar,
		}

		if c.strategy == "" {
			c.strategy = StrategyRedact
		}
		switch c.strategy {
		case StrategyRedact, StrategyMask, StrategyPartial, StrategyHash:
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown strategy %q", name, r.Strategy))
		}

		if c.keepLast == 0 {
			c.keepLast = DefaultKeepLast
		} else if c.keepLast < 0 {
			problems = append(problems, fmt.Sprintf("%s: negative keep_last %d", name, r.KeepLast))
		}
		if c.maskChar == 0 {
			c.maskChar = DefaultMaskChar
		} else if !utf8.ValidRune(c.maskChar) {
			problems = append(problems, fmt.Sprintf("%s: mask_char is not a valid rune (%d)", name, r.MaskChar))
		}

		switch {
		case r.Entity == "":
			// Without a name the audit event cannot say what was masked, so
			// this is a config error even when a Pattern is present.
			problems = append(problems, name+": no entity")
		case r.Pattern != "":
			re, err := regexp.Compile(r.Pattern)
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("%s: bad pattern: %v", name, err))
			case re.MatchString(""):
				// An empty match claims a zero-width span at every offset and
				// rewrites nothing, so it would look like a working rule while
				// masking nothing at all.
				problems = append(problems, name+": pattern matches the empty string")
			default:
				c.re = re
			}
		case detected[r.Entity]:
			entity := string(r.Entity)
			c.detect = func(data []byte) [][2]int { return d.Find(entity, data) }
		default:
			b, ok := builtins[r.Entity]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: unknown entity %q (known: %s%s; set pattern_regex for a custom one)",
					name, r.Entity, strings.Join(knownEntities, ", "), detectorHint(detected)))
				break
			}
			c.re, c.validate = b.re, b.validate
		}

		if c.strategy == StrategyRedact {
			c.redaction = []byte("[REDACTED:" + string(r.Entity) + "]")
		}
		out = append(out, c)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("mask: invalid rules: %s", strings.Join(problems, "; "))
	}
	return &Masker{rules: out}, nil
}

// Mask rewrites every matched span in data.
//
// When nothing matches it returns data itself, not a copy, so the common case
// of a clean payload costs one scan and no allocation. Callers must therefore
// treat the result as aliasing the input.
func (m *Masker) Mask(data []byte) ([]byte, Result) {
	hits := make([]bool, len(m.rules))
	out, n := m.mask(data, hits)
	return out, m.result(hits, n)
}

// MaskString is Mask over a string. It copies the input, so prefer Mask when
// the payload is already bytes.
func (m *Masker) MaskString(s string) (string, Result) {
	out, res := m.Mask([]byte(s))
	return string(out), res
}

// MaskJSON masks the STRING VALUES of a JSON document and never its keys.
//
// Masking a key breaks the client's parser and buys no privacy: "ssn" is not
// sensitive, the digits under it are. Values that are JSON numbers are also
// left alone — rewriting one either produces invalid JSON or silently changes
// a quantity, and a caller who stores card numbers as JSON numbers has a
// bigger problem than this function can fix.
//
// On invalid JSON it returns the input unchanged along with the error, so a
// caller can fall back to Mask over the raw bytes. Check the error, not the
// output, to tell the two apart.
//
// Two properties of the round-trip are worth knowing: object key order is not
// preserved (Go maps are unordered and encoding/json emits them sorted), and
// insignificant whitespace is dropped. The document is semantically identical,
// not byte-identical.
func (m *Masker) MaskJSON(data []byte) ([]byte, Result, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// Without UseNumber every number round-trips through float64, which
	// silently mangles exactly the values this package exists to protect: a
	// 16-digit card number stored as a JSON number comes back as 4.111e+15.
	dec.UseNumber()

	var doc any
	if err := dec.Decode(&doc); err != nil {
		return data, Result{}, fmt.Errorf("mask: invalid json: %w", err)
	}
	if dec.More() {
		// Re-marshalling would silently drop everything after the first
		// value, so refuse rather than truncate the payload.
		return data, Result{}, fmt.Errorf("mask: invalid json: trailing data after top-level value")
	}

	hits := make([]bool, len(m.rules))
	count := 0
	doc = m.walk(doc, hits, &count)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The payload is a database result set, not a web page. HTML escaping
	// would rewrite &, < and > inside untouched values for no reason.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return data, Result{}, fmt.Errorf("mask: re-encode: %w", err)
	}
	out := buf.Bytes()
	return out[:len(out)-1], m.result(hits, count), nil // Encode appends a newline
}

// walk masks string leaves in place. Map keys are traversed but never
// rewritten.
func (m *Masker) walk(v any, hits []bool, count *int) any {
	switch t := v.(type) {
	case string:
		out, n := m.mask([]byte(t), hits)
		*count += n
		if n == 0 {
			return t // avoid the string copy when nothing changed
		}
		return string(out)
	case []any:
		for i, e := range t {
			t[i] = m.walk(e, hits, count)
		}
	case map[string]any:
		for k, e := range t {
			t[k] = m.walk(e, hits, count)
		}
	}
	// json.Number, bool and nil fall through untouched.
	return v
}

// span is a claimed region of the input and the rule that claimed it.
type span struct {
	start, end int
	rule       int
}

// mask is the shared core: claim spans, then render. It marks hits[i] for
// every rule that rewrote something and returns the number of spans.
func (m *Masker) mask(data []byte, hits []bool) ([]byte, int) {
	spans := m.claim(data)
	if len(spans) == 0 {
		return data, 0
	}

	out := make([]byte, 0, len(data))
	prev := 0
	for _, sp := range spans {
		out = append(out, data[prev:sp.start]...)
		out = m.rules[sp.rule].apply(out, data[sp.start:sp.end])
		hits[sp.rule] = true
		prev = sp.end
	}
	return append(out, data[prev:]...), len(spans)
}

// claim runs every rule in order and returns the non-overlapping spans to
// rewrite, sorted by start offset.
//
// Earlier rules win: a span that overlaps one already claimed is dropped
// entirely rather than trimmed, because half a credit card number rewritten by
// a different strategy is neither masked nor readable. A span a validator
// rejects is never claimed, so a later rule may still match that text.
func (m *Masker) claim(data []byte) []span {
	var claimed []span
	// try is the shared tail of both paths: validate the span, then insert
	// it if it does not overlap one an earlier rule already took.
	try := func(ri, s, e int) {
		r := &m.rules[ri]
		if r.validate != nil && !r.validate(string(data[s:e])) {
			return
		}
		i, ok := insertPos(claimed, s, e)
		if !ok {
			return
		}
		claimed = append(claimed, span{})
		copy(claimed[i+1:], claimed[i:])
		claimed[i] = span{start: s, end: e, rule: ri}
	}

	for ri := range m.rules {
		r := &m.rules[ri]
		if r.detect == nil {
			for _, loc := range r.re.FindAllIndex(data, -1) {
				try(ri, loc[0], loc[1])
			}
			continue
		}
		for _, loc := range r.detect(data) {
			s, e := loc[0], loc[1]
			// A detector-supplied span is outside the Masker's control, so
			// check it addresses real bytes before slicing with it. A buggy
			// detector must mask nothing, never panic the relay.
			if s < 0 || e > len(data) || s >= e {
				continue
			}
			try(ri, s, e)
		}
	}
	return claimed
}

// insertPos returns where [s,e) belongs in the start-sorted claimed slice, and
// false if it overlaps a neighbour.
func insertPos(claimed []span, s, e int) (int, bool) {
	i := sort.Search(len(claimed), func(i int) bool { return claimed[i].start >= s })
	if i < len(claimed) && claimed[i].start < e {
		return 0, false
	}
	if i > 0 && claimed[i-1].end > s {
		return 0, false
	}
	return i, true
}

// apply appends the rewritten form of value to dst.
func (c *compiled) apply(dst, value []byte) []byte {
	switch c.strategy {
	case StrategyRedact:
		return append(dst, c.redaction...)

	case StrategyMask:
		// Rune count, not byte count: the caller cares about display width,
		// and a multi-byte mask char over a multi-byte value would otherwise
		// change both.
		for range string(value) {
			dst = utf8.AppendRune(dst, c.maskChar)
		}
		return dst

	case StrategyPartial:
		keep := c.keepLast
		if n := utf8.RuneCount(value); keep >= n {
			// Keeping the whole value is not partial masking, it is no
			// masking. Fail towards the safe end.
			keep = 0
		} else {
			keep = n - keep // convert to a rune index of the first kept rune
		}
		i := 0
		for _, r := range string(value) {
			switch {
			case keep != 0 && i >= keep:
				dst = utf8.AppendRune(dst, r)
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				dst = utf8.AppendRune(dst, c.maskChar)
			default:
				// Separators are format, not data. Keeping them is what makes
				// the output recognizable as a card number.
				dst = utf8.AppendRune(dst, r)
			}
			i++
		}
		return dst

	case StrategyHash:
		sum := sha256.Sum256(value)
		dst = append(dst, "sha256:"...)
		var buf [16]byte
		hex.Encode(buf[:], sum[:8])
		return append(dst, buf[:]...)
	}
	// Unreachable: New rejects any other strategy.
	return append(dst, value...)
}

// result turns per-rule hit flags into the operator-facing summary.
func (m *Masker) result(hits []bool, n int) Result {
	if n == 0 {
		return Result{}
	}
	var names []string
	seen := make(map[Entity]bool, len(hits))
	for i, hit := range hits {
		e := m.rules[i].entity
		if !hit || seen[e] {
			continue
		}
		seen[e] = true
		names = append(names, string(e))
	}
	return Result{Entities: names, Count: n}
}
