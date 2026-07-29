package alcatraz

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hoophq/alcatraz"
	"github.com/hoophq/alcatraz/anonymizer"
	"github.com/hoophq/hoopinspect/gate"
)

// Strategy says how a detected span is rewritten.
type Strategy string

const (
	// StrategyRedact replaces the value with "[REDACTED:<entity>]". The
	// output tells the reader something was removed and what kind of thing it
	// was, which is what stops a support engineer filing a bug about
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
	// Equal inputs give equal outputs, so a masked column still works as a
	// join key and a GROUP BY still counts distinct users. The cost is real
	// and worth stating plainly: the mapping is deterministic and unsalted,
	// so it LEAKS EQUALITY (you can tell two rows share an email without
	// learning it) and is trivially reversed by dictionary attack over any
	// small value space. Hashing a US SSN is pointless — there are only 10^9
	// of them. Use it for high-entropy identifiers where correlation is the
	// requirement, and redact everything else.
	StrategyHash Strategy = "hash"
)

// DefaultKeepLast is the StrategyPartial tail length when KeepLast is unset.
// Four is the number printed on receipts, so it is the number people expect.
const DefaultKeepLast = 4

// DefaultMaskChar is the replacement character when MaskChar is unset.
const DefaultMaskChar = '*'

// Rule pairs an entity type with the rewrite applied to it.
type Rule struct {
	// Name identifies the rule in configuration errors. Defaults to
	// "rule[<index>]".
	Name string `json:"name,omitempty"`

	// Entity is the alcatraz entity type this rule rewrites, named as the
	// constants in github.com/hoophq/alcatraz/entities ("US_SSN", "BR_CPF")
	// or one of AWSAccessKey / JWT / PrivateKey. It is what appears in
	// Result.Entities and therefore in the audit trail.
	Entity string `json:"entity"`

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
}

// Result reports what a Mask call rewrote.
//
// It deliberately carries no values: an audit log recording the values it
// masked has un-masked them, and it is the log that gets shipped off-box to a
// search cluster.
type Result struct {
	// Entities names the distinct entity classes that were rewritten, sorted
	// so a log query can rely on the order.
	Entities []string `json:"entities,omitempty"`

	// Count is how many spans were rewritten.
	Count int `json:"count"`
}

// Masker rewrites detected values out of a payload.
//
// Detection is alcatraz's engine; rewriting is alcatraz's anonymizer. This
// type is the adapter between them and the byte-oriented interface a relay
// needs — gate.Masker in hoopinspect.
//
// Immutable after NewMasker and safe for concurrent use by any number of
// connections.
type Masker struct {
	eng  *alcatraz.Engine
	opts alcatraz.Options
	cfg  anonymizer.Config

	// entities is the sorted set the rules cover, and the Analyze restriction.
	entities []string
}

// NewMasker compiles a rule set against a detector's engine.
//
// It reports EVERY invalid rule in one error rather than stopping at the
// first. A masking config is edited by hand and deployed to a fleet; finding
// out about the second typo on the next restart is how a rollout takes three
// rounds instead of one. And it fails at construction, not on the first
// request that trips the bad rule — a masker that silently passes a payload
// through is worse than one that refuses to start.
//
// The rules restrict what is looked for: a payload is scanned only for the
// entity types some rule rewrites, so an unused recognizer costs nothing.
func NewMasker(d *Detector, rules []Rule) (*Masker, error) {
	if d == nil {
		return nil, fmt.Errorf("alcatraz: NewMasker needs a Detector")
	}

	claims := make(map[string]bool, len(d.active))
	for _, e := range d.active {
		claims[e] = true
	}

	var problems []string
	perEntity := make(map[string]anonymizer.Operator, len(rules))
	seen := make(map[string]string, len(rules))
	entities := make([]string, 0, len(rules))

	for i, r := range rules {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("rule[%d]", i)
		}

		switch {
		case r.Entity == "":
			// Without a name the audit event cannot say what was masked.
			problems = append(problems, name+": no entity")
			continue
		case !claims[r.Entity]:
			problems = append(problems, fmt.Sprintf(
				"%s: entity %q is not in the detector's set (configured: %s)",
				name, r.Entity, strings.Join(d.active, ", ")))
			continue
		}

		// Two rules for one entity is ambiguous, not additive: the anonymizer
		// keys operators by entity type, so the second would silently replace
		// the first.
		if prev, dup := seen[r.Entity]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s: entity %q already rewritten by %s", name, r.Entity, prev))
			continue
		}

		op, err := r.operator(name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}

		seen[r.Entity] = name
		perEntity[r.Entity] = op
		entities = append(entities, r.Entity)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("mask: invalid rules: %s", strings.Join(problems, "; "))
	}
	sort.Strings(entities)

	opts := d.opts
	opts.Entities = entities

	return &Masker{
		eng:      d.eng,
		opts:     opts,
		entities: entities,
		cfg: anonymizer.Config{
			// Every entity has an explicit rule, so the default is only
			// reached if alcatraz reports a type we did not ask for. Redact
			// is the safe answer to that.
			Default:   redactOperator(),
			PerEntity: perEntity,
		},
	}, nil
}

// operator turns one rule into the anonymizer Operator that renders it.
func (r Rule) operator(name string) (anonymizer.Operator, error) {
	keepLast := r.KeepLast
	switch {
	case keepLast == 0:
		keepLast = DefaultKeepLast
	case keepLast < 0:
		return nil, fmt.Errorf("%s: negative keep_last %d", name, r.KeepLast)
	}

	maskChar := r.MaskChar
	switch {
	case maskChar == 0:
		maskChar = DefaultMaskChar
	case !utf8.ValidRune(maskChar):
		return nil, fmt.Errorf("%s: mask_char is not a valid rune (%d)", name, r.MaskChar)
	}

	strategy := r.Strategy
	if strategy == "" {
		strategy = StrategyRedact
	}

	switch strategy {
	case StrategyRedact:
		return redactOperator(), nil
	case StrategyMask:
		return anonymizer.Mask(maskChar), nil
	case StrategyPartial:
		return partialOperator(maskChar, keepLast), nil
	case StrategyHash:
		return hashOperator(), nil
	}
	return nil, fmt.Errorf("%s: unknown strategy %q", name, r.Strategy)
}

// redactOperator renders "[REDACTED:<entity>]".
func redactOperator() anonymizer.Operator {
	return func(entity, _ string) string { return "[REDACTED:" + entity + "]" }
}

// partialOperator keeps the last keep runes and masks the ALPHANUMERIC ones
// before them, leaving punctuation in place.
//
// This is deliberately not anonymizer.MaskKeepLast, which masks every
// preceding rune including separators: it turns 4111-1111-1111-1234 into
// ***************1234. Keeping the dashes is what makes the output legible as
// a card number rather than a corrupted string, and legible output is what
// stops the support ticket.
func partialOperator(maskChar rune, keep int) anonymizer.Operator {
	return func(_, match string) string {
		n := utf8.RuneCountInString(match)
		cut := n - keep
		if keep >= n {
			// Keeping the whole value is not partial masking, it is no
			// masking. Fail towards the safe end.
			cut = n
		}
		var b strings.Builder
		b.Grow(len(match))
		for i, r := range []rune(match) {
			switch {
			case i >= cut:
				b.WriteRune(r)
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(maskChar)
			default:
				// Separators are format, not data.
				b.WriteRune(r)
			}
		}
		return b.String()
	}
}

// hashOperator renders "sha256:<first 16 hex digits>".
func hashOperator() anonymizer.Operator {
	return func(_, match string) string {
		sum := sha256.Sum256([]byte(match))
		var buf [16]byte
		hex.Encode(buf[:], sum[:8])
		return "sha256:" + string(buf[:])
	}
}

// Mask rewrites every detected span in data.
//
// When nothing matches it returns data itself, not a copy, so the common case
// of a clean payload costs one scan and no allocation. Callers must therefore
// treat the result as aliasing the input.
func (m *Masker) Mask(data []byte) ([]byte, Result) {
	if len(data) == 0 || len(m.entities) == 0 {
		return data, Result{}
	}

	text := string(data)
	results := m.eng.Analyze(text, m.opts)
	if len(results) == 0 {
		return data, Result{}
	}

	out := anonymizer.AnonymizeWith(text, results, m.cfg)

	names := make([]string, 0, len(m.entities))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if seen[r.EntityType] {
			continue
		}
		seen[r.EntityType] = true
		names = append(names, r.EntityType)
	}
	sort.Strings(names)

	return []byte(out), Result{Entities: names, Count: len(results)}
}

// MaskString is Mask over a string.
func (m *Masker) MaskString(s string) (string, Result) {
	out, res := m.Mask([]byte(s))
	return string(out), res
}

// Entities returns the entity types this Masker rewrites, sorted.
func (m *Masker) Entities() []string {
	return append([]string(nil), m.entities...)
}

// BuildMasker decodes the sidecar's "mask" config section and returns a
// gate.Masker. It is how a Detector satisfies the sidecar's Plugin interface.
//
// The JSON is a plain array of Rule. Decoding is strict: an unknown field is a
// typo, and a typo that silently disables a masking rule is the failure this
// package exists to avoid.
func (d *Detector) BuildMasker(rawRules []byte) (gate.Masker, error) {
	if len(rawRules) == 0 {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(rawRules))
	dec.DisallowUnknownFields()
	var rules []Rule
	if err := dec.Decode(&rules); err != nil {
		return nil, fmt.Errorf("mask.rules: %w", err)
	}
	if len(rules) == 0 {
		return nil, nil
	}

	m, err := NewMasker(d, rules)
	if err != nil {
		return nil, err
	}
	return maskAdapter{m}, nil
}

// maskAdapter bridges *Masker to the narrow gate.Masker interface, which
// reports names and a count rather than a Result.
type maskAdapter struct{ m *Masker }

func (a maskAdapter) Mask(data []byte) ([]byte, []string, int) {
	out, res := a.m.Mask(data)
	return out, res.Entities, res.Count
}
