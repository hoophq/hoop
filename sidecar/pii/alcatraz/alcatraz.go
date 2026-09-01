// Package alcatraz plugs github.com/hoophq/alcatraz into sidecar as both a
// masking Detector and a policy Scanner.
//
// # A separate module
//
// The sidecar root module carries only libhoop, so you can audit it
// without a supply-chain review and drop it into a caller without touching
// their dependency tree. Alcatraz is itself dependency-free, but importing it
// from the root would add a module edge to every consumer of the library,
// including the ones with no use for 51 national-ID recognizers. So it lives
// here, behind the two interfaces the root already declares, the same shape
// as store/sqlite.
//
// # Coverage
//
// Alcatraz brings 51 entity types across 12 countries, and 25 of them carry a
// real checksum validator: Luhn, ISO 7064 mod-97 for IBAN, Verhoeff for
// Aadhaar, the Brazilian mod-11 schemes. For a deployment whose data is not
// US-shaped, that decides whether masking works at all.
//
// It is a PII engine, so it has no recognizer for a credential. secrets.go
// adds three (AWS_ACCESS_KEY, JWT, PRIVATE_KEY) into the same engine, so a
// config names them exactly like a built-in alcatraz type. AllEntities()
// therefore reports 54.
//
//	det, err := alcatraz.NewDetector(alcatraz.Options{
//	    Entities: []string{entities.BRCPF, entities.IBANCode},
//	})
//	m, err := alcatraz.NewMasker(det, rules)            // response masking
//	p, err := policy.NewRulesWithScanner(rules, det)    // request guardrails
//
// One Detector drives both, so a deployment configures its entity list once.
//
// # An empty entity list means all of them
//
// Options.Entities is optional, and leaving it empty activates all 54
// recognizers. That is safe because enabling a recognizer is not the same as
// scanning for it. Two layers below, somebody names entities, and that name
// is what decides the scan:
//
//   - NewMasker narrows the engine to exactly the entity types its own rules
//     name (masker.go, opts.Entities = entities). A permissive Detector
//     driving a two-rule Masker scans a response for two entities.
//   - A pii guardrail hands its own entity list to ScanTextFor, which
//     intersects it with the active set. A permissive Detector under a rule
//     naming CREDIT_CARD scans a statement for CREDIT_CARD.
//
// Naming a noisy entity in a MASK RULE is still the mistake the Noisy map
// warns about, because that IS the act that puts the recognizer on a data
// path: a US_SSN mask rule redacts about a third of a column of nine-digit
// order ids no matter how the Detector was built. Options.Ignored is the
// pairing for the permissive form, subtracting the recognizers this
// deployment's ordinary data trips.
//
// # Pattern core only
//
// This wires the pattern-and-checksum core. Alcatraz's PERSON, LOCATION and
// NRP entities need the optional alcatraz/ner module, which loads an ONNX
// model: a different deployment shape (hundreds of MB of weights, per-call
// inference latency on the response path) and a different decision. Adding it
// later stays open, because ner plugs into the same engine and costs one
// constructor option.
package alcatraz

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hoophq/alcatraz"
	"github.com/hoophq/alcatraz/analyzer"
	"github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/alcatraz/recognizers"
	"github.com/hoophq/hoop/sidecar/policy"
)

// DefaultThreshold drops detections scoring below it. 0.4 is alcatraz's own
// CLI default.
//
// It removes the weak heuristics (US_BANK_NUMBER scores 0.05, US_PASSPORT
// 0.30) that fire constantly on ordinary numeric data. It does NOT tame a
// recognizer whose validator promotes a hit to 1.0, and a validator only
// rejects what its format can check: an SSN carries no checksum, so any nine
// digits in a legal area/group/serial range verify. Measured over random
// nine-digit business ids, US_SSN fires on about a third of them at any
// threshold.
//
// The threshold is therefore not what keeps a noisy recognizer off a data
// path. The rule that names the entity is. See Noisy and Options.Entities.
const DefaultThreshold = 0.4

// Noisy names the recognizers that fire often on ordinary business data, so a
// caller enabling one does it knowing the cost.
//
// The rates below were measured over synthetic order ids, SKUs and reference
// numbers, no PII at all, at DefaultThreshold:
//
//	US_SSN       ~32%   nine digits in a legal range; no checksum exists
//	ABA_ROUTING  ~2.5%  nine digits with a weak mod-10
//	AU_TFN       ~2.1%  eight-to-nine digits with a weak checksum
//	AU_ACN       ~2.0%  nine digits with a weak checksum
//	US_ITIN      ~1.7%  nine digits in a narrower range
//	URL          high   every HTTP response body has one
//	DATE_TIME    high   every row has a timestamp
//
// Every rate here is correct behavior. A nine-digit SSN is indistinguishable
// from a nine-digit order id, and a detector that refused to report it would
// miss every real one. Whoever knows the schema decides which columns hold
// order ids and which hold identifiers.
var Noisy = map[string]string{
	entities.USSSN:      "~32% of random 9-digit ids; SSNs carry no checksum",
	entities.ABARouting: "~2.5% of random 9-digit ids",
	entities.AUTFN:      "~2.1% of random 8-9 digit ids",
	entities.AUACN:      "~2.0% of random 9-digit ids",
	entities.USITIN:     "~1.7% of random 9-digit ids",
	entities.URL:        "every HTTP response body contains one",
	entities.DateTime:   "every row with a timestamp",
}

// Options configures a Detector.
type Options struct {
	// Entities selects the alcatraz entity types to detect, named as the
	// constants in github.com/hoophq/alcatraz/entities ("US_SSN", "BR_CPF").
	//
	// Empty means every supported type, all 54 of them. The package
	// documentation carries the argument for why that is not the trap it
	// reads as: NewMasker and ScanTextFor both narrow the scan to the
	// entities their own caller named, so an active recognizer nobody
	// names costs nothing on either data path.
	//
	// The row that the "all entities" default is accused of corrupting,
	//
	//	{"order_id":457555462,"customer_id":123456781}
	//
	// loses both integers to a US_SSN MASK RULE, because nine digits in a
	// legal range IS a valid SSN as far as any detector can tell. Writing
	// that rule is the mistake. Leaving this field empty is not.
	//
	// Name the types your data contains when you know them: it documents
	// the deployment, and it keeps ScanText, the one path that does run
	// the whole active set, narrow. Use Ignored when you do not.
	Entities []string

	// Ignored removes types from the active set, applied after Entities.
	// It is the knob for a permissive Detector: name the seven recognizers
	// in Noisy that fire on this deployment's ordinary data rather than
	// enumerating the 47 that do not.
	//
	// A name alcatraz does not know is refused here exactly as it is in
	// Entities. Subtraction fails silent: a misspelled entry removes
	// nothing, leaves the recognizer it was written to disable running,
	// and the deployment learns about it from a masked production column.
	Ignored []string

	// Threshold drops detections scoring below it. Zero means
	// DefaultThreshold. Negative means no threshold, which admits alcatraz's
	// low-confidence heuristics and is rarely what you want on a data path.
	Threshold float64

	// AllowList suppresses detections whose matched text is in this list.
	// The usual case is a test fixture (a documentation card number, a
	// seeded example account) that would otherwise be masked out of every
	// staging response.
	AllowList []string

	// Language selects the recognizer set. Empty means "en". The built-in
	// recognizers all detect language-independent structured identifiers, so
	// this only matters once a language-specific recognizer is added.
	Language string
}

// Detector adapts an alcatraz engine to sidecar's masking and policy
// interfaces. Safe for concurrent use.
type Detector struct {
	eng  *alcatraz.Engine
	opts alcatraz.Options

	// active is the resolved entity set, sorted, and doubles as the
	// Entities() answer.
	active []string
}

var (
	_ policy.Scanner = (*Detector)(nil)
	_ Plugin         = (*Detector)(nil)

	// The optional narrowing interface. It is optional to implement and
	// only ever reached through a type assertion, so nothing would fail to
	// compile if the method drifted out of shape. This line would.
	_ policy.ScopedScanner = (*Detector)(nil)
)

// newEngine builds the alcatraz engine this package uses: the full built-in
// recognizer set plus the credential recognizers in secrets.go.
//
// One builder keeps AllEntities and NewDetector from disagreeing about what
// is registered. A mismatch there would reject a valid config with "unknown
// entity type".
func newEngine(lang string) *alcatraz.Engine {
	reg := analyzer.NewRegistry(lang)
	recognizers.LoadDefaults(reg, lang)
	registerSecrets(reg, lang)
	return analyzer.NewEngine(reg, []string{lang})
}

// AllEntities returns every entity type this package detects: alcatraz's 51
// built-ins plus AWS_ACCESS_KEY, JWT and PRIVATE_KEY, 54 in all.
//
// NewDetector activates the same set for an empty Options.Entities, so this
// exists for a caller that wants the list itself: to print it at startup, to
// diff a config against it, or to write "everything" somewhere an empty
// slice would read as an oversight.
func AllEntities() []string {
	return newEngine("en").SupportedEntities("en")
}

// NewDetector builds a Detector over the named entity types. An empty
// Options.Entities means every supported type; see that field for why the
// permissive form is safe.
//
// It returns an error when Entities or Ignored names a type alcatraz cannot
// detect, and when Ignored subtracts the last remaining one. All three are
// config mistakes that would otherwise surface as masking doing something
// other than what the config says, which is the failure mode this package
// exists to avoid. The two spelling checks report together, so an operator
// who mistyped in both lists fixes both on one restart.
func NewDetector(o Options) (*Detector, error) {
	lang := o.Language
	if lang == "" {
		lang = "en"
	}
	eng := newEngine(lang)

	known := make(map[string]bool)
	for _, e := range eng.SupportedEntities(lang) {
		known[e] = true
	}
	// Both lists are checked, and Ignored is the one that needs it more.
	// A bad name in Entities narrows the set to something the operator did
	// not ask for; a bad name in Ignored subtracts NOTHING and leaves the
	// recognizer it was written to disable running over every response.
	// Neither failure is visible from the outside, and the permissive
	// default makes the second the likelier of the two: the whole reason
	// to write the section is to take a recognizer away.
	//
	// Both are reported at once, and each names the list it came from, so
	// the operator edits the right key rather than grepping for the name.
	var problems []string
	if bad := unknownIn(known, o.Entities); len(bad) > 0 {
		problems = append(problems, "entities: "+strings.Join(bad, ", "))
	}
	if bad := unknownIn(known, o.Ignored); len(bad) > 0 {
		problems = append(problems, "ignored: "+strings.Join(bad, ", "))
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("alcatraz: unknown entity type(s) in %s "+
			"(PERSON, LOCATION and NRP need the alcatraz/ner module, which this package does not wire)",
			strings.Join(problems, "; "))
	}

	// An empty list is the permissive form: every recognizer the engine
	// registered. The scan is narrowed by whoever names entities, in
	// NewMasker for a response and in ScanTextFor for a statement, so the
	// only path that pays for the full set is ScanText.
	active := o.Entities
	if len(active) == 0 {
		active = eng.SupportedEntities(lang)
	}
	if len(o.Ignored) > 0 {
		ignored := o.Ignored
		skip := make(map[string]bool, len(ignored))
		for _, e := range ignored {
			skip[e] = true
		}
		kept := make([]string, 0, len(active))
		for _, e := range active {
			if !skip[e] {
				kept = append(kept, e)
			}
		}
		active = kept
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("alcatraz: Ignored removed every entity in Entities")
	}

	// Copy before sorting: Options belongs to the caller.
	active = append([]string(nil), active...)
	sort.Strings(active)

	threshold := o.Threshold
	if threshold == 0 {
		threshold = DefaultThreshold
	}

	d := &Detector{
		eng:    eng,
		active: active,
		opts: alcatraz.Options{
			Entities:  active,
			Language:  lang,
			AllowList: o.AllowList,
		},
	}
	if threshold > 0 {
		d.opts.Threshold = &threshold
	}
	return d, nil
}

// unknownIn returns the names in want that the engine does not recognize,
// sorted and deduplicated so one typo written twice reads as one mistake.
// Nil when every name resolves, which is the case it is called in.
func unknownIn(known map[string]bool, want []string) []string {
	var bad []string
	for _, e := range want {
		if !known[e] {
			bad = append(bad, e)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return slices.Compact(bad)
}

// Entities returns the active entity types. The returned slice is a copy: a caller
// sorting or trimming it must not reshape the Detector.
func (d *Detector) Entities() []string {
	return append([]string(nil), d.active...)
}

// Find returns the byte spans of one entity type in data.
//
// It restricts the engine to the single entity asked for rather than running
// the whole active set and discarding the rest. The Masker calls this once per
// rule, so a three-rule config costs three narrow scans instead of three full
// ones. Nothing is cached between calls either, which matters because a cache
// keyed on payloads is a map holding the PII it just found.
//
// Alcatraz reports byte offsets into the analyzed string, and Go's
// []byte-to-string conversion preserves them, so spans need no translation.
func (d *Detector) Find(entity string, data []byte) [][2]int {
	if len(data) == 0 || !d.claims(entity) {
		return nil
	}

	opts := d.opts
	opts.Entities = []string{entity}

	results := d.eng.Analyze(string(data), opts)
	if len(results) == 0 {
		return nil
	}
	spans := make([][2]int, 0, len(results))
	for _, r := range results {
		spans = append(spans, [2]int{r.Start, r.End})
	}
	// Alcatraz orders by score, then offset. The Masker wants ascending
	// start order.
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	return spans
}

// claims reports whether entity is in the active set. Find is only ever
// called for an entity Entities() advertised, but hand-wiring the Detector
// can get that wrong, and a narrow scan costs less than trusting the caller.
func (d *Detector) claims(entity string) bool {
	i := sort.SearchStrings(d.active, entity)
	return i < len(d.active) && d.active[i] == entity
}

// ScanText implements policy.Scanner, naming the entity classes present.
//
// It returns names only, never values or offsets: a policy verdict travels
// into an audit record, and a denial quoting the identifier it denied has
// published it.
func (d *Detector) ScanText(text string) []string {
	if text == "" {
		return nil
	}
	results := d.eng.Analyze(text, d.opts)
	if len(results) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(results))
	out := make([]string, 0, len(results))
	for _, r := range results {
		if seen[r.EntityType] {
			continue
		}
		seen[r.EntityType] = true
		out = append(out, r.EntityType)
	}
	sort.Strings(out)
	return out
}

// ScanTextFor is ScanText restricted to the entity classes the caller can act
// on, implementing the root module's optional policy.ScopedScanner.
//
// A guardrail rule names its own entity list and throws away every finding
// outside it, so the recognizers for the rest run for nothing. That is a
// rounding error when a config names five entities and real work when it
// names none: a permissive Detector has all 54 active, and paying for 54
// recognizer passes per statement to keep two answers is the one genuine
// cost of the permissive default. This is how a caller declines to pay it.
//
// entities is intersected with the active set rather than trusted, for the
// reason Find calls claims: a rule naming a class this Detector does not
// carry must narrow the scan to nothing, never widen it. An empty argument
// asks for no narrowing at all and scans the active set, which is what the
// ScopedScanner contract says it means.
func (d *Detector) ScanTextFor(entities []string, text string) []string {
	if text == "" {
		return nil
	}
	opts := d.opts
	if len(entities) > 0 {
		scan := make([]string, 0, len(entities))
		for _, e := range entities {
			if d.claims(e) {
				scan = append(scan, e)
			}
		}
		if len(scan) == 0 {
			// Nothing this Detector could find would survive the caller's
			// own filter, so the engine call is pure cost.
			return nil
		}
		opts.Entities = scan
	}

	results := d.eng.Analyze(text, opts)
	if len(results) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(results))
	out := make([]string, 0, len(results))
	for _, r := range results {
		if seen[r.EntityType] {
			continue
		}
		seen[r.EntityType] = true
		out = append(out, r.EntityType)
	}
	sort.Strings(out)
	return out
}

// Supported reports the entity types this Detector will look for, as a
// human-readable list for a config error or a startup log line.
func (d *Detector) Supported() string { return strings.Join(d.active, ", ") }
