// Package alcatraz plugs github.com/hoophq/alcatraz into sidecar as both a
// masking Detector and a policy Scanner.
//
// # A separate module
//
// The sidecar root module carries only libhoop, so you can audit it
// without a supply-chain review and drop it into a caller without touching
// their dependency tree. Alcatraz is itself dependency-free, but importing it
// from the root would add a module edge to every consumer of the library,
// including the ones with no use for 45 national-ID recognizers. So it lives
// here, behind the two interfaces the root already declares, the same shape
// as store/sqlite.
//
// # Coverage
//
// Alcatraz brings 45 entity types across 12 countries, and 25 of them carry a
// real checksum validator: Luhn, ISO 7064 mod-97 for IBAN, Verhoeff for
// Aadhaar, the Brazilian mod-11 schemes. For a deployment whose data is not
// US-shaped, that decides whether masking works at all.
//
// It is a PII engine, so it has no recognizer for a credential. secrets.go
// adds three (AWS_ACCESS_KEY, JWT, PRIVATE_KEY) into the same engine, so a
// config names them exactly like a built-in alcatraz type.
//
//	det, err := alcatraz.NewDetector(alcatraz.Options{
//	    Entities: []string{entities.BRCPF, entities.IBANCode},
//	})
//	m, err := alcatraz.NewMasker(det, rules)            // response masking
//	p, err := policy.NewRulesWithScanner(rules, det)    // request guardrails
//
// One Detector drives both, so a deployment configures its entity list once.
//
// # Name the entities you want
//
// Options.Entities is required and there is no all-entities default. Turning
// on all 45 recognizers rewrites ordinary numeric columns: nine digits in a
// legal range is a valid US_SSN as far as any detector can tell, so a row of
// order ids comes back redacted. Measured on synthetic business ids, US_SSN
// alone fires on about a third of them. The Noisy map records the worst
// offenders with their measured rates, and AllEntities() exists for a caller
// who has read it and still wants everything.
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
// So Options.Entities is required rather than defaulted. See its
// documentation.
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
	// REQUIRED. There is no "all entities" default. Enabling all 45
	// recognizers on a response path corrupts ordinary data: a row like
	//
	//	{"order_id":457555462,"customer_id":123456781}
	//
	// has both integers rewritten as US_SSN, because nine digits in a legal
	// range IS a valid SSN as far as any detector can tell. An operator
	// switches off a masker that mangles a third of the numeric columns
	// within a day, and then nothing is masked at all.
	//
	// So you name the entity types your data contains. See Noisy for the
	// ones that cost the most when guessed at, and AllEntities if you want
	// the full set.
	Entities []string

	// Ignored removes types from the active set, applied after Entities.
	// Chiefly useful with AllEntities.
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

// AllEntities returns every entity type this package detects: alcatraz's
// built-in set plus AWS_ACCESS_KEY, JWT and PRIVATE_KEY.
//
// Prefer naming the types your data contains. This exists so "everything" is
// an explicit, greppable decision rather than the consequence of leaving a
// config field blank.
func AllEntities() []string {
	return newEngine("en").SupportedEntities("en")
}

// NewDetector builds a Detector over the named entity types.
//
// It returns an error when Entities is empty or names a type alcatraz cannot
// detect. Both are config mistakes that would otherwise surface as "masking
// silently does nothing", which is the failure mode this package exists to
// avoid.
func NewDetector(o Options) (*Detector, error) {
	lang := o.Language
	if lang == "" {
		lang = "en"
	}
	eng := newEngine(lang)

	if len(o.Entities) == 0 {
		return nil, fmt.Errorf("alcatraz: Options.Entities is required " +
			"(there is no safe all-entities default; use AllEntities() to opt in explicitly)")
	}

	known := make(map[string]bool)
	for _, e := range eng.SupportedEntities(lang) {
		known[e] = true
	}
	var unknown []string
	for _, e := range o.Entities {
		if !known[e] {
			unknown = append(unknown, e)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("alcatraz: unknown entity type(s) %s "+
			"(PERSON, LOCATION and NRP need the alcatraz/ner module, which this package does not wire)",
			strings.Join(unknown, ", "))
	}

	active := o.Entities
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

// Entities returns the active entity types. The returned slice is a copy: a caller
// sorting or trimming it must not reshape the Detector.
func (d *Detector) Entities() []string {
	return append([]string(nil), d.active...)
}

// Find returns the byte spans of one entity type in data.
//
// It restricts the engine to the single entity asked for rather than running
// all 45 recognizers and discarding the rest. The Masker calls this once per
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

// Supported reports the entity types this Detector will look for, as a
// human-readable list for a config error or a startup log line.
func (d *Detector) Supported() string { return strings.Join(d.active, ", ") }
