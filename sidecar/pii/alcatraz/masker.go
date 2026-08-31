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
	"github.com/hoophq/hoop/sidecar/gate"
)

// Strategy says how a detected span is rewritten.
type Strategy string

const (
	// StrategyRedact replaces the value with "[REDACTED:<entity>]". The
	// output tells the reader something was removed and what kind of thing it
	// was, which stops a support engineer filing a bug about corrupted data.
	StrategyRedact Strategy = "redact"

	// StrategyMask replaces every character with MaskChar, preserving the
	// rune count. Use it when the consumer parses fixed-width columns and a
	// length change would shift every field after it.
	StrategyMask Strategy = "mask"

	// StrategyPartial keeps the last KeepLast characters and masks the
	// alphanumeric ones before them, leaving punctuation in place:
	// 4111-1111-1111-1234 becomes ****-****-****-1234. A human uses the tail
	// to confirm "yes, that is the card I meant", the only reason to show any
	// of it.
	StrategyPartial Strategy = "partial"

	// StrategyHash replaces the value with "sha256:<first 16 hex digits>".
	//
	// Equal inputs give equal outputs, so a masked column still works as a
	// join key and a GROUP BY still counts distinct users. The cost is real:
	// the mapping is deterministic and unsalted, so it LEAKS EQUALITY (you
	// can tell two rows share an email without learning it) and falls to a
	// dictionary attack over any small value space. Hashing a US SSN buys
	// nothing, since only 10^9 of them exist. Use it for high-entropy
	// identifiers where correlation is the requirement, and redact
	// everything else.
	StrategyHash Strategy = "hash"
)

// DefaultKeepLast is the StrategyPartial tail length when KeepLast is unset.
// Four is the number printed on receipts, so it is the number people expect.
const DefaultKeepLast = 4

// DefaultMaskChar is the replacement character when MaskChar is unset.
const DefaultMaskChar = '*'

// Rule says what to rewrite and how.
//
// A rule matches EITHER by detected entity type or by column name. Both are
// useful and they answer different questions:
//
//	{"entities": ["US_SSN"], "strategy": "partial"}   // wherever an SSN appears
//	{"columns": ["ssn"], "strategy": "redact"}        // whatever is in that column
//
// The column form is available only where the protocol names its values (a
// database result set) and it is stronger there. It is deterministic rather
// than probabilistic, it protects a column whose contents no detector
// recognizes (an internal risk score, a free-text note), and it does not care
// that alcatraz declines 123-45-6789 as a placeholder.
type Rule struct {
	// Name identifies the rule in configuration errors. Defaults to
	// "rule[<index>]".
	Name string `json:"name,omitempty"`

	// Entities lists the alcatraz entity types this rule rewrites, named as
	// the constants in github.com/hoophq/alcatraz/entities ("US_SSN",
	// "BR_CPF") or one of AWSAccessKey / JWT / PrivateKey. Three entities
	// in one rule compile to three rewrites sharing one strategy, which is
	// the whole reason the plural exists: a contact-details rule covers an
	// email and a phone number without saying "redact" twice.
	//
	// Required unless Columns is set, and it cannot be combined with
	// Columns. An entity named beside columns is only a label for the
	// audit trail, and a list of labels names nothing.
	Entities []string `json:"entities,omitempty"`

	// Entity is the one-entity spelling of Entities.
	//
	// Deprecated: use Entities. The field stays declared because
	// BuildMasker decodes with DisallowUnknownFields, so deleting it would
	// refuse every config already deployed instead of migrating it.
	// Setting both spellings on one rule is an error rather than a silent
	// winner. Unlike Entities it may still be combined with Columns, where
	// it supplies the audit label for the masked cells.
	Entity string `json:"entity,omitempty"`

	// Columns names result-set columns to mask outright, compared
	// case-insensitively. Ignored for protocols that do not name their
	// values; a rule with only Columns therefore never fires on HTTP.
	//
	// The whole cell is rewritten, not a span within it: if the operator
	// says the column is sensitive, its contents are sensitive whether or
	// not a detector agrees.
	Columns []string `json:"columns,omitempty"`

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

// entityName is the label a column rule's masked cells are reported under.
// Only the deprecated singular reaches it, because Entities and Columns
// cannot appear on one rule, so there is never a list to choose from here.
func (r Rule) entityName() string {
	if r.Entity != "" {
		return r.Entity
	}
	// A column-only rule still needs a name in the audit trail. "column:ssn"
	// says both what happened and why, without inventing an entity type that
	// no detector produces.
	return "column:" + strings.ToLower(strings.Join(r.Columns, ","))
}

// Result reports what a Mask call rewrote.
//
// It carries no values: an audit log recording the values it masked has
// un-masked them, and that log gets shipped off-box to a search cluster.
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
// type adapts them to the byte-oriented interface a relay needs,
// gate.Masker in the sidecar module.
//
// Immutable after NewMasker and safe for concurrent use by any number of
// connections.
type Masker struct {
	eng  *alcatraz.Engine
	opts alcatraz.Options
	cfg  anonymizer.Config

	// entities is the sorted set the rules cover, and the Analyze restriction.
	entities []string

	// byColumn holds the column-name rules, keyed lowercased. Only consulted
	// by MaskCell: a protocol that does not name its values cannot match
	// these, and Mask over an opaque blob never sees a column.
	byColumn map[string]columnRule
}

// NewMasker compiles a rule set against a detector's engine.
//
// It reports EVERY invalid rule in one error rather than stopping at the
// first. Someone edits a masking config by hand and deploys it to a fleet;
// finding out about the second typo on the next restart is how a rollout
// takes three rounds instead of one. It fails at construction, before the
// first request that trips the bad rule, because a masker that silently
// passes a payload through is worse than one that refuses to start.
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
	byColumn := make(map[string]columnRule, len(rules))

	for i, r := range rules {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("rule[%d]", i)
		}

		op, err := r.operator(name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}

		// One spelling from here down. The deprecated singular survives so
		// a config written before the rename still loads, and setting both
		// is refused rather than resolved: picking a winner silently is
		// how a rename disables the control an operator thought they kept.
		ents := r.Entities
		if r.Entity != "" {
			if len(ents) > 0 {
				problems = append(problems, name+
					": set entities, not both entity and entities")
				continue
			}
			ents = []string{r.Entity}
		}

		// A column rule needs no detector: the operator has already decided
		// the column is sensitive, so there is nothing to detect.
		if len(r.Columns) > 0 {
			// An entity named beside columns is a LABEL for the masked
			// cells and nothing else, so a LIST of them says nothing: two
			// labels for one cell has no meaning to pick between. The
			// singular keeps working here for the configs that already
			// pair the two.
			if len(r.Entities) > 0 {
				problems = append(problems, name+
					": entities cannot be combined with columns (entity names the audit label for a column rule)")
				continue
			}
			label := r.entityName()
			for _, col := range r.Columns {
				key := strings.ToLower(strings.TrimSpace(col))
				if key == "" {
					problems = append(problems, name+": empty column name")
					continue
				}
				if prev, dup := byColumn[key]; dup {
					problems = append(problems, fmt.Sprintf(
						"%s: column %q already masked by %s", name, key, prev.rule))
					continue
				}
				byColumn[key] = columnRule{op: op, entity: label, rule: name}
			}
			// An entity named alongside columns labels them; it does not also
			// enable content detection, which would be two rules in one.
			continue
		}

		if len(ents) == 0 {
			// Without either, the rule matches nothing and the audit event
			// could not say what was masked.
			problems = append(problems, name+": no entity or columns")
			continue
		}

		// One rule over three entities is three rewrites sharing one
		// operator. Every check below runs per entity and against the maps
		// the whole set shares, so a rule that names an entity another rule
		// already claimed still collides, and a rule that repeats an entity
		// collides with itself.
		for _, e := range ents {
			if !claims[e] {
				problems = append(problems, fmt.Sprintf(
					"%s: entity %q is not in the detector's set (configured: %s)",
					name, e, strings.Join(d.active, ", ")))
				continue
			}
			// Two rules for one entity is ambiguous: the anonymizer keys
			// operators by entity type, so the second would silently replace
			// the first.
			if prev, dup := seen[e]; dup {
				problems = append(problems, fmt.Sprintf(
					"%s: entity %q already rewritten by %s", name, e, prev))
				continue
			}

			seen[e] = name
			perEntity[e] = op
			entities = append(entities, e)
		}
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
		byColumn: byColumn,
		cfg: anonymizer.Config{
			// Every entity has an explicit rule, so the default is only
			// reached if alcatraz reports a type we did not ask for. Redact
			// is the safe answer to that.
			Default:   redactOperator(),
			PerEntity: perEntity,
		},
	}, nil
}

// columnRule is a compiled Columns entry: which operator rewrites the cell,
// what to call it in the audit trail, and which rule to blame in an error.
type columnRule struct {
	op     anonymizer.Operator
	entity string
	rule   string
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
// This avoids anonymizer.MaskKeepLast, which masks every preceding rune
// including separators: it turns 4111-1111-1111-1234 into
// ***************1234. Keeping the dashes leaves the output legible as a card
// number rather than a corrupted string, and legible output stops the support
// ticket.
func partialOperator(maskChar rune, keep int) anonymizer.Operator {
	return func(_, match string) string {
		n := utf8.RuneCountInString(match)
		cut := n - keep
		if keep >= n {
			// Keeping the whole value masks nothing. Fail towards the
			// safe end.
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
// A clean payload comes back as data itself, not a copy, so the common case
// costs one scan and no allocation. Callers must treat the result as
// aliasing the input.
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

// MaskCell rewrites one already-delimited value from a named column.
//
// This is the database path, where the protocol supplies more than Mask gets.
// Mask scans an opaque blob for anything that LOOKS sensitive; MaskCell is
// told where the value ends and what the server calls it. So it can honor a
// rule like {"columns": ["ssn"]} deterministically, with no detector involved
// and no argument about whether 123-45-6789 is a real SSN or a fixture.
//
// Precedence: a column rule wins outright and the cell is not scanned at all.
// The operator naming a column has already made the decision, and running a
// detector afterwards could only disagree with them. Otherwise the cell falls
// back to entity detection, which is Mask restricted to one value.
func (m *Masker) MaskCell(column string, value []byte) ([]byte, []string, int) {
	if len(value) == 0 {
		return value, nil, 0
	}

	if cr, ok := m.byColumn[strings.ToLower(column)]; ok {
		return []byte(cr.op(cr.entity, string(value))), []string{cr.entity}, 1
	}

	if len(m.entities) == 0 {
		return value, nil, 0
	}
	out, res := m.Mask(value)
	return out, res.Entities, res.Count
}

// Columns returns the column names this Masker rewrites outright, sorted.
func (m *Masker) Columns() []string {
	out := make([]string, 0, len(m.byColumn))
	for c := range m.byColumn {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
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

func (a maskAdapter) MaskCell(column string, value []byte) ([]byte, []string, int) {
	return a.m.MaskCell(column, value)
}
