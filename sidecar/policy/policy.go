// Package policy turns an inspected Statement into an allow/deny verdict.
//
// Two evaluators ship here, and they layer:
//
//   - Rules: a local, dependency-free matcher (deny-words, regex, operation
//     and table allow/deny lists). Microseconds, no network, so it is safe on
//     the data path. Use it for the coarse "never, under any circumstances"
//     rules.
//   - OPA: a client for Open Policy Agent's Data API. Use it for policy an
//     InfoSec team already owns in Rego.
//
// # Deny messages reach the user
//
// Envoy's RBAC network filter denies by dropping the connection. The developer
// sees "connection reset" and files a ticket. Every deny path here carries an
// operator-authored Message, and the caller must surface it in the protocol's
// own error frame (a Postgres ErrorResponse, a MySQL ERR packet) so the user
// reads the reason and fixes it themselves.
//
// # Fail-closed
//
// Both evaluators fail closed on error by default: if OPA is unreachable or a
// rule cannot compile, the verdict is Deny with a diagnostic Message. Set
// FailOpen to invert that where availability outranks enforcement. Neither
// evaluator has a third mode.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Verdict is the outcome of evaluating one statement.
type Verdict struct {
	// Denied is the decision. False means the statement may proceed.
	Denied bool

	// Message reaches the end user when Denied. The operator authors it
	// where a rule supplies one, so it can name the actual constraint
	// ("destructive statements are not permitted on appdb") rather than
	// leaking rule internals.
	Message string

	// Rule identifies which rule produced a denial, for audit correlation.
	// Empty on allow.
	Rule string

	// Err holds the failure when evaluation itself broke (OPA unreachable,
	// bad regex). Denied reflects the fail-open/fail-closed choice; Err
	// records the cause.
	Err error

	// Annotations carry evaluator-specific facts for the audit record.
	// They never affect the decision: Denied is the whole of that.
	//
	// The field exists because the AI analyzer produces something no other
	// evaluator does: a risk level that is worth recording on an ALLOWED
	// statement. Widening Denied into a severity would change every
	// evaluator; a side channel the gate copies onto the event does not.
	//
	// Keys must be a small fixed vocabulary, because audit.SinkOptions
	// redaction does not reach Event.Metadata: anything put here bypasses
	// redact_statements and lands in the trail verbatim. Never put a value
	// the statement contained here.
	Annotations map[string]string
}

// Allow is the zero verdict.
func Allow() Verdict { return Verdict{} }

// Deny builds a denial carrying a user-facing message.
func Deny(rule, msg string) Verdict {
	return Verdict{Denied: true, Message: msg, Rule: rule}
}

// Evaluator produces a verdict for a statement. Rules and OPAClient both
// implement it, and Chain composes them.
type Evaluator interface {
	Evaluate(stmt inspect.Statement) Verdict
}

// EvalContext threads facts between the evaluators in one Chain run.
//
// It exists because an evaluator that establishes a fact and an evaluator
// that decides what the fact means are usually not the same one. A PII
// scanner knows which entity classes a statement carries; a model knows how
// risky it looks; neither should also be the thing that rules on it, because
// the ruling depends on the actor, the hour and the table, and that belongs
// in one policy rather than scattered across YAML.
//
// Nothing here knows what a producer is. A producer writes a Finding, a later
// evaluator reads it, and this struct is the whole of the hop between them.
type EvalContext struct {
	// Annotations are the merged annotations of every evaluator that has
	// already run, under the same vocabulary and the same merge rules as
	// the Annotations on the returned Verdict. It is the same map.
	//
	// This is the AUDIT channel: flat strings, one fixed vocabulary, copied
	// verbatim onto the event. Findings is the policy channel. Keeping them
	// apart is what stops the trail's shape from dictating what a policy
	// can be told.
	Annotations map[string]string

	// Findings are what producers established, keyed by Source.
	//
	// One entry per source. A source with several rules on one lane folds
	// its own findings, because only that source knows whether two of its
	// verdicts combine by taking the worst, the union or the last: see
	// Finding.Merge for the default and AddFinding for the seam.
	Findings map[string]Finding

	// Requested records which sources a policy asked to run, by Source.
	//
	// Three-valued per source, which is why it is a map of bool rather than
	// a set. Absent is "no opinion": the producer's own configuration
	// decides, which is what every lane did before a gate existed. True
	// runs it regardless. False vetoes a run its configuration would have
	// made, so a policy taking the decision over can take all of it rather
	// than only widening it.
	Requested map[string]bool

	// Context carries per-connection facts an evaluator may need: the
	// authenticated user, the connection name, a correlation id.
	//
	// It rides here rather than on the evaluator because one OPAClient is
	// shared by every connection on a lane, and a chain may hold two of
	// them. Copying the client per statement to stamp a user on it scales
	// with neither.
	Context map[string]string
}

// Finding is one producer's contribution to a decision it does not make.
//
// The shape is deliberately thin. Source and Status are this package's
// business because a reader has to be able to tell "the scanner found
// nothing" from "the scanner did not run" without knowing what a scanner is.
// Values belongs entirely to the producer: policy neither reads it nor
// validates it, which is what keeps a new producer from costing a line here.
type Finding struct {
	// Source names the producer, and is the key a policy addresses it by.
	// Stable: it is part of the input document, so renaming one breaks
	// somebody's Rego.
	Source string `json:"-"`

	// Rule names the configured rule that produced this, for correlation
	// with the audit trail and for a policy that wants per-rule treatment.
	Rule string `json:"rule,omitempty"`

	// Status is one of the Finding* constants. Always set.
	Status string `json:"status"`

	// Reason narrows a non-ok Status where the producer has something
	// useful to say: which budget, which limit. Free text for a human,
	// never load-bearing for a policy.
	//
	// NOT omitempty, deliberately. An undefined reference makes the whole
	// Rego rule undefined, so `sprintf("...", [f.reason])` inside a
	// fail-closed rule silently deletes that rule when the key is absent;
	// the exact statement it was written to refuse then sails through. One
	// empty string per finding in a decision log is the cheaper problem.
	Reason string `json:"reason"`

	// Values is the producer's own payload, serialized into the input
	// document as-is.
	//
	// It must hold only facts ABOUT the statement, never content FROM it.
	// A policy engine's decision log is a copy of everything sent to it, so
	// "the statement carries a taxpayer id" belongs here and the id does
	// not.
	//
	// Absent when the producer established nothing, which is why a policy
	// guards on Status before reading it rather than testing a value and
	// reading the absence as a negative.
	Values map[string]any `json:"values,omitempty"`
}

// Statuses a Finding can carry. A policy keys on these, so they are a public
// contract in the same way the input document's field names are.
const (
	// FindingOK means the producer ran and its Values are authoritative.
	FindingOK = "ok"

	// FindingCached means the same, from a cache rather than a fresh run.
	// Separate from ok so an operator can watch a hit rate, and equivalent
	// to it for every policy purpose.
	FindingCached = "cached"

	// FindingSkipped means the producer chose not to run: nothing matched
	// its trigger, or a policy vetoed it.
	FindingSkipped = "skipped"

	// FindingUnavailable means the producer could not run for a reason that
	// is not a failure: a spent budget, a refusal to transmit.
	FindingUnavailable = "unavailable"

	// FindingError means the producer tried and broke.
	FindingError = "error"
)

// FindingRank orders statuses by how little the producer established.
//
// Exported because every producer that can emit two findings for one
// statement needs the same "most degraded wins" fold, and a second copy of
// this ordering is how one of them ends up reporting healthy through an
// outage. An unrecognized status ranks zero so it never displaces a real one.
func FindingRank(status string) int {
	switch status {
	case FindingError:
		return 4
	case FindingUnavailable:
		return 3
	case FindingSkipped:
		return 2
	case FindingCached, FindingOK:
		return 1
	}
	return 0
}

// Answered reports whether the producer established its Values.
//
// The question a policy asks most, and the one it most often gets wrong by
// testing a value instead: an absent value means "found nothing", "never
// ran", "budget spent" and "provider down" all at once.
func (f Finding) Answered() bool {
	return f.Status == FindingOK || f.Status == FindingCached
}

// Merge folds other into f, keeping the more degraded status and preferring
// the answered side's Values.
//
// It is the default a producer gets by calling AddFinding, and it is only a
// default: a producer whose two rules combine differently (a union of entity
// classes, a highest risk level) folds them itself and writes the result.
func (f Finding) Merge(other Finding) Finding {
	if FindingRank(other.Status) > FindingRank(f.Status) {
		// The degraded side wins the status, but must not erase values
		// the answered side established.
		out := other
		if !other.Answered() && f.Answered() {
			out.Values = f.Values
			out.Rule = f.Rule
		}
		return out
	}
	if !f.Answered() && other.Answered() {
		f.Values = other.Values
		f.Rule = other.Rule
	}
	return f
}

// AddFinding records f, folding it into any finding already present for the
// same Source with Finding.Merge.
//
// A producer that needs different combining semantics reads Findings[source]
// itself, folds, and calls this with the result.
func (e *EvalContext) AddFinding(f Finding) {
	if e.Findings == nil {
		e.Findings = make(map[string]Finding, 2)
	}
	if prev, ok := e.Findings[f.Source]; ok {
		f = prev.Merge(f)
	}
	e.Findings[f.Source] = f
}

// Finding returns what a source established, and whether it reported at all.
func (e *EvalContext) Finding(source string) (Finding, bool) {
	if e == nil {
		return Finding{}, false
	}
	f, ok := e.Findings[source]
	return f, ok
}

// WantsRun reports whether a policy asked for source to run, and whether it
// expressed any opinion at all. A producer with no opinion against it falls
// back to its own configuration.
func (e *EvalContext) WantsRun(source string) (want, stated bool) {
	if e == nil {
		return false, false
	}
	want, stated = e.Requested[source]
	return want, stated
}

// ContextualEvaluator is an Evaluator that reads what earlier evaluators in
// the Chain established, and may write back for later ones.
//
// Optional: Chain calls EvaluateWith where it is implemented and Evaluate
// everywhere else, so Evaluator stays a one-method interface and Rules never
// has to know a context exists.
type ContextualEvaluator interface {
	Evaluator

	// EvaluateWith evaluates against a context the Chain owns. An
	// implementation must not retain ec: it lives for one statement.
	EvaluateWith(stmt inspect.Statement, ec *EvalContext) Verdict
}

// --- local rules ---------------------------------------------------------

// MatchType selects how a Rule matches.
type MatchType string

const (
	// MatchDenyWords denies when the statement text contains any of Words,
	// case-insensitively. This is the blunt instrument: "no DROP, ever".
	MatchDenyWords MatchType = "deny_words_list"

	// MatchPattern denies when Pattern matches the statement text.
	MatchPattern MatchType = "pattern_match"

	// MatchOperation denies when the normalized operation is in Operations.
	// Prefer this over deny-words for verbs: it is immune to a DELETE hiding
	// in a string literal or a comment, because Operation comes from the
	// classifier, which strips both.
	MatchOperation MatchType = "operation"

	// MatchTable denies when the statement references any table in Tables.
	// Because Tables is best-effort (see inspect.ClassifySQL), a rule of
	// this type also denies when RequireTableMatch is set and no tables could
	// be extracted: "we could not tell" reads as unsafe.
	MatchTable MatchType = "table"

	// MatchAIAnalysis sends the statement to a language model and denies
	// according to the risk it reports.
	//
	// It is the only rule type Rules does NOT evaluate. The sidecar lifts
	// rules of this type out of the set before building Rules and turns
	// each one into an analyzer.Evaluator appended after the local rules
	// and OPA, so a statement a free rule already denies never reaches a
	// paid classifier. Rules rejects one that reaches it, because a rule
	// that parses and never fires is the failure this package refuses
	// everywhere else.
	MatchAIAnalysis MatchType = "ai_analysis"
)

// AITrigger narrows which statements an ai_analysis rule sends to a model.
//
// It is declared here rather than in the analyzer package so the config
// schema has one home: a Rule is what the YAML decodes into, and a trigger
// living elsewhere would mean two packages owning one config block.
type AITrigger struct {
	// Operations matches the statement's normalized verb.
	Operations []inspect.Operation `json:"operations,omitempty"`

	// Tables matches any referenced table. Because Tables is best effort, a
	// statement whose tables could not be determined does NOT match; key a
	// load-bearing trigger on operations.
	Tables []string `json:"tables,omitempty"`

	// Resources matches an HTTP resource glob, using the same matcher an
	// http_resource rule uses.
	Resources []string `json:"resources,omitempty"`
}

// IsZero reports whether the trigger names nothing.
func (t *AITrigger) IsZero() bool {
	return t == nil || (len(t.Operations) == 0 && len(t.Tables) == 0 && len(t.Resources) == 0)
}

// Rule is one local matcher.
type Rule struct {
	// Name identifies the rule in Verdict.Rule and in audit output.
	Name string `json:"name"`

	// Type selects the matching strategy.
	Type MatchType `json:"type"`

	// Words for MatchDenyWords.
	Words []string `json:"words,omitempty"`

	// Pattern for MatchPattern, an RE2 regular expression.
	Pattern string `json:"pattern_regex,omitempty"`

	// Operations for MatchOperation.
	Operations []inspect.Operation `json:"operations,omitempty"`

	// Tables for MatchTable, compared lowercased. A bare name matches any
	// schema qualification: "customers" matches "public.customers".
	Tables []string `json:"tables,omitempty"`

	// RequireTableMatch makes a MatchTable rule also deny statements whose
	// tables could not be determined. Set it when the rule protects something
	// that must never be touched, and accept the false positives.
	RequireTableMatch bool `json:"require_table_match,omitempty"`

	// Access narrows a MatchTable rule to "write" or "read". Empty matches
	// either, which is what every rule written before the split meant.
	//
	// It is the difference between "nothing writes to customers" and
	// "nothing mentions customers". Without it, a rule protecting a table
	// fires on `INSERT INTO staging SELECT * FROM customers`, which only
	// reads it, and operators learn to widen the rule until it protects
	// nothing.
	Access string `json:"access,omitempty"`

	// Message reaches the user on denial. Leave it empty and the rule falls
	// back to a generated message naming only the rule and the operation;
	// set it.
	Message string `json:"message,omitempty"`

	// Action is what a match does. Empty denies, which is what every rule
	// did before producers could report, and stays the default so a config
	// written against the old behavior means the same thing.
	//
	// ActionDefer reports the match as a Finding and lets a later evaluator
	// rule on it. The MATCHING stays here (microseconds, no network, a
	// regex the local engine already compiled) and only the DETERMINATION
	// moves. That split is the point: rewriting `deny_words_list` as Rego
	// over input.statement duplicates the matcher, while deferring keeps
	// one matcher and one decision-maker.
	//
	// Deferring does not stop the rule set. First match wins applies to
	// DENIALS; a deferring rule records and evaluation continues, so a
	// later hard rule still denies and a policy sees every match.
	//
	// Not valid on ai_analysis, which expresses the same thing per risk
	// level through high/medium/low.
	Action string `json:"action,omitempty"`

	// Entities for MatchPII, naming the classes a Scanner must not find.
	Entities []string `json:"entities,omitempty"`

	// Trigger narrows which statements a MatchAIAnalysis rule classifies.
	// A rule with an empty trigger classifies nothing, because the failure
	// mode of "matches everything by accident" is a bill rather than an
	// error.
	Trigger *AITrigger `json:"trigger,omitempty"`

	// HighRisk, MediumRisk and LowRisk map a MatchAIAnalysis verdict onto
	// an action: allow, warn or block. An unset level defaults to allow, so
	// an operator opts into blocking a tier by naming it.
	HighRisk   string `json:"high,omitempty"`
	MediumRisk string `json:"medium,omitempty"`
	LowRisk    string `json:"low,omitempty"`

	// Prompt replaces the analyzer's default risk guidance for this rule.
	//
	// Per rule rather than per process, because what counts as risky is a
	// property of what a rule protects: a customer ledger and an orders API
	// want different wording, and both can sit in one config. This is also
	// the only place protocol-specific wording belongs: analyzer.prompt
	// reaches every lane, so SQL advice written there follows an HTTP
	// statement to the model. Empty inherits analyzer.prompt, and an empty
	// analyzer.prompt uses the built-in guidance, which covers both
	// protocols.
	//
	// It replaces the GUIDANCE only. The output contract (call exactly one
	// tool, never quote a literal value from the statement) is appended
	// after it and cannot be removed, because a title repeating the
	// identifier it objected to has published that identifier into the
	// audit trail.
	Prompt string `json:"prompt,omitempty"`

	// HTTP-specific fields (Resources, Statuses, Fields, MaxDepth, ...).
	// Embedded so one ordered rule set can mix SQL and HTTP matchers; a
	// deployment fronting both a database and an API should not need two
	// evaluators with two orderings.
	httpRuleFields

	// compiled caches the Pattern regex.
	compiled *regexp.Regexp
}

// Rules is an ordered set of local rules. The first match wins, so order
// expresses precedence.
type Rules struct {
	rules []Rule

	// scanner backs MatchPII rules. It is nil unless NewRulesWithScanner set
	// it, and NewRules rejects PII rules while it is nil.
	scanner Scanner

	// FailOpen inverts the error behavior: when a rule cannot be evaluated
	// (an invalid regex), allow instead of deny. Default false.
	FailOpen bool
}

// NewRules compiles a rule set. It returns an error listing every rule that
// failed to compile, so a bad config surfaces at startup rather than on the
// first request that happens to hit it.
func NewRules(rules []Rule) (*Rules, error) { return newRules(rules, false) }

// newRules is the shared constructor. hasScanner tells PII validation whether
// a Scanner will be attached, so a PII rule without one fails at startup
// instead of quietly allowing every statement.
func newRules(rules []Rule, hasScanner bool) (*Rules, error) {
	var problems []string
	out := make([]Rule, 0, len(rules))

	for i, r := range rules {
		if r.Name == "" {
			r.Name = fmt.Sprintf("rule[%d]", i)
		}
		switch r.Action {
		case "", ActionDefer:
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: unknown action %q (empty denies, %q reports a finding)",
				r.Name, r.Action, ActionDefer))
		}
		if r.Action == ActionDefer && r.Type == MatchAIAnalysis {
			// The analyzer defers per risk level, and a rule saying both
			// would leave two answers for one question.
			problems = append(problems, r.Name+
				": ai_analysis defers per risk level through high/medium/low, not through action")
		}
		switch r.Type {
		case MatchDenyWords:
			if len(r.Words) == 0 {
				problems = append(problems, r.Name+": deny_words_list with no words")
			}
		case MatchPattern:
			if r.Pattern == "" {
				problems = append(problems, r.Name+": pattern_match with no pattern")
				break
			}
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: bad pattern: %v", r.Name, err))
				break
			}
			r.compiled = re
		case MatchOperation:
			if len(r.Operations) == 0 {
				problems = append(problems, r.Name+": operation rule with no operations")
			}
		case MatchTable:
			if len(r.Tables) == 0 {
				problems = append(problems, r.Name+": table rule with no tables")
			}
			switch r.Access {
			case "", string(inspect.AccessRead), string(inspect.AccessWrite):
			default:
				problems = append(problems, fmt.Sprintf(
					"%s: unknown access %q (read, write, or empty for either)",
					r.Name, r.Access))
			}
		case MatchPII:
			if err := r.validatePII(hasScanner); err != nil {
				problems = append(problems, err.Error())
			}
		case MatchAIAnalysis:
			// Rules cannot evaluate this type: it needs a provider, a
			// deadline and a cache, none of which belong to a local
			// matcher. The sidecar lifts these rules out before
			// building a Rules, so one arriving here means it was
			// constructed by a caller that does not know to do that,
			// and silently accepting it would leave a guardrail that
			// loads, evaluates and matches nothing.
			problems = append(problems, r.Name+
				": ai_analysis rules are evaluated by the analyzer, not by the local rule set")
		case MatchHTTPResource, MatchHTTPStatus:
			if err := r.validateHTTP(); err != nil {
				problems = append(problems, err.Error())
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown rule type %q", r.Name, r.Type))
		}
		out = append(out, r)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("policy: invalid rules: %s", strings.Join(problems, "; "))
	}
	return &Rules{rules: out}, nil
}

// ActionDefer reports a match as a Finding instead of denying it.
//
// The string is shared with the analyzer's action vocabulary on purpose: an
// operator writing `action: defer` on a deny-words rule and `high: defer` on
// an ai_analysis rule is asking for the same thing, and two spellings of one
// idea is one too many.
const ActionDefer = "defer"

// Evaluate implements Evaluator. First match wins.
func (r *Rules) Evaluate(stmt inspect.Statement) Verdict {
	return r.EvaluateWith(stmt, &EvalContext{})
}

// EvaluateWith implements ContextualEvaluator.
//
// First match wins among rules that DENY. Deferring rules record and
// evaluation continues, so one statement can report several findings and
// still be denied by a hard rule further down.
func (r *Rules) EvaluateWith(stmt inspect.Statement, ec *EvalContext) Verdict {
	// Findings accumulate per rule TYPE rather than per rule, because a
	// policy asks "what did the PII scanner find", not "what did the rule
	// named no-cpf find". The rule names ride along inside.
	var deferred map[MatchType]Finding

	// Flushed on EVERY exit, including a denial. What the scanner found is
	// true whether or not a later rule refused the statement, and dropping
	// it on the deny path would make the record depend on rule ORDER: move
	// the hard rule above the deferring one and the finding disappears.
	defer func() {
		if ec == nil {
			return
		}
		for _, f := range deferred {
			ec.AddFinding(f)
		}
	}()

	for _, rule := range r.rules {
		// Rules owns the Scanner, so PII dispatches here rather than
		// inside Rule.matches.
		if rule.Type == MatchPII {
			hit, entities := rule.matchesPII(stmt, r.scanner)
			if !hit {
				continue
			}
			if rule.Action == ActionDefer {
				deferred = recordMatch(deferred, rule, map[string]any{
					"entities": entities,
				})
				continue
			}
			return Deny(rule.Name, rule.piiMessage(entities))
		}

		matched, err := rule.matches(stmt)
		if err != nil {
			if r.FailOpen {
				return Verdict{Err: err}
			}
			return Verdict{
				Denied:  true,
				Message: "policy evaluation failed; denying",
				Rule:    rule.Name,
				Err:     err,
			}
		}
		if !matched {
			continue
		}
		if rule.Action == ActionDefer {
			deferred = recordMatch(deferred, rule, rule.findingValues(stmt))
			continue
		}
		return Deny(rule.Name, rule.messageOr(stmt))
	}
	return Allow()
}

// recordMatch folds one matching rule into its type's finding.
//
// Values merge by union so two rules of one type report the whole of what
// they saw between them. Overwriting would mean a second pii rule matching
// one entity class hides the first rule's three.
func recordMatch(into map[MatchType]Finding, rule Rule, values map[string]any) map[MatchType]Finding {
	if into == nil {
		into = make(map[MatchType]Finding, 1)
	}
	f, seen := into[rule.Type]
	if !seen {
		f = Finding{
			Source: string(rule.Type),
			Rule:   rule.Name,
			Status: FindingOK,
			Values: map[string]any{},
		}
	}
	f.Values["rules"] = appendUnique(stringsIn(f.Values["rules"]), rule.Name)
	for k, v := range values {
		if list, ok := v.([]string); ok {
			f.Values[k] = appendUnique(stringsIn(f.Values[k]), list...)
			continue
		}
		f.Values[k] = v
	}
	into[rule.Type] = f
	return into
}

// findingValues is what a matched rule can tell a policy that the input
// document does not already carry.
//
// Most rule types answer "nothing": operation, table and the HTTP pair match
// on fields Rego can already read off input, so the finding's value is the
// match itself. deny_words is the exception worth naming, because which of
// several words fired is not recoverable from input.statement without
// reimplementing the matcher.
//
// A matched pattern's TEXT is never reported. It is content from the
// statement, and OPA's decision log is a copy of everything sent to it.
func (r Rule) findingValues(stmt inspect.Statement) map[string]any {
	if r.Type != MatchDenyWords {
		return nil
	}
	var hit []string
	upper := strings.ToUpper(stmt.Text)
	for _, w := range r.Words {
		if w != "" && strings.Contains(upper, strings.ToUpper(w)) {
			hit = append(hit, w)
		}
	}
	return map[string]any{"words": hit}
}

func stringsIn(v any) []string {
	out, _ := v.([]string)
	return out
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		if !slices.Contains(dst, a) {
			dst = append(dst, a)
		}
	}
	return dst
}

func (r Rule) messageOr(stmt inspect.Statement) string {
	if r.Message != "" {
		return r.Message
	}
	return fmt.Sprintf("statement denied by policy rule %q (operation=%s)", r.Name, stmt.Operation)
}

func (r Rule) matches(stmt inspect.Statement) (bool, error) {
	// HTTP rule types are handled in http.go; ok=false means "not mine".
	if matched, ok := r.matchesHTTP(stmt); ok {
		return matched, nil
	}
	switch r.Type {
	case MatchDenyWords:
		upper := strings.ToUpper(stmt.Text)
		for _, w := range r.Words {
			if w == "" {
				continue
			}
			if strings.Contains(upper, strings.ToUpper(w)) {
				return true, nil
			}
		}
		return false, nil

	case MatchPattern:
		if r.compiled == nil {
			return false, fmt.Errorf("policy: rule %q has no compiled pattern", r.Name)
		}
		return r.compiled.MatchString(stmt.Text), nil

	case MatchOperation:
		for _, op := range r.Operations {
			if stmt.Operation == op {
				return true, nil
			}
		}
		return false, nil

	case MatchTable:
		if len(stmt.Relations) == 0 && len(stmt.Tables) == 0 {
			// Could not determine the relations. Deny only when the rule
			// opted into that strictness.
			return r.RequireTableMatch, nil
		}
		for _, want := range r.Tables {
			want = strings.ToLower(want)
			// Relations carries the access; Tables does not. Prefer it
			// where the codec supplied it, so `access: write` means
			// something, and fall back for a caller-built Statement that
			// only set Tables.
			if len(stmt.Relations) > 0 {
				for _, got := range stmt.Relations {
					if !r.accessMatches(got.Access) {
						continue
					}
					if got.Name == want || strings.HasSuffix(got.Name, "."+want) {
						return true, nil
					}
				}
				continue
			}
			for _, got := range stmt.Tables {
				if got == want || strings.HasSuffix(got, "."+want) {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("policy: unknown rule type %q", r.Type)
}

// accessMatches reports whether a relation's access satisfies the rule.
//
// An unset Access matches both, which is what every rule written before the
// split meant and the only reading that keeps them working.
func (r Rule) accessMatches(got inspect.Access) bool {
	switch r.Access {
	case "":
		return true
	case string(inspect.AccessWrite):
		return got == inspect.AccessWrite
	case string(inspect.AccessRead):
		return got == inspect.AccessRead
	}
	return true
}

// --- chaining ------------------------------------------------------------

// Chain evaluates every evaluator in order and returns the first denial.
//
// The intended order is local rules first, OPA second: a statement that a
// local rule already forbids never costs a network round-trip, and OPA stays
// off the hot path for the obvious cases.
//
// # Only a denial stops the chain
//
// An evaluator reports a broken evaluation through Err and decides for
// itself whether that means deny; a fail-open evaluator returns
// Denied=false with Err set. Treating a non-nil Err as a stop condition
// would let a fail-open evaluator suppress every evaluator behind it, so one
// unreachable OPA or one uncompilable regex would silently disable the rest
// of the policy, the opposite of what an operator asked for by choosing
// fail-open for availability.
//
// So Err accumulates and evaluation continues. The errors travel on the
// returned Verdict either way, including on a denial, because the caller
// audits them and a degraded evaluator is worth recording even when a later
// one denied anyway.
type Chain []Evaluator

// Annotation keys with defined merge semantics.
//
// Only keys whose merge Chain has to know about live here. A producer's own
// vocabulary belongs in the producer's package: the analyzer's ai_status is
// merged by the analyzer before it is written, so Chain never has to rank it
// and this package never has to name it.
//
// These two survive the rule because store/ reads risk_level to roll a
// session's highest risk up onto its record, so the key and its
// highest-wins fold are load-bearing outside the producer.
const (
	// AnnotationRiskLevel is one of low, medium or high.
	AnnotationRiskLevel = "risk_level"

	// AnnotationRiskAction is what that level mapped to. It is meaningful
	// only beside the level it came from, which is why it merges as part
	// of a unit with the level.
	AnnotationRiskAction = "risk_action"
)

// riskRank orders risk levels for a highest-wins merge. An unrecognized
// level ranks zero so it never displaces a real one.
func riskRank(level string) int {
	switch level {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

// mergeAnnotations folds src into dst and returns the result.
//
// Every key is last-write-wins except risk, which is highest-wins and travels
// with its action as a PAIR. A lane may carry several producers emitting the
// same two keys, so last-write-wins would let one that rated a statement low
// erase one that rated it high. The audit record carries a single risk_level
// and the session rollup keeps the highest across statements, so a downgrade
// here silently understates the session.
//
// The pair moves together because an action is only meaningful beside the
// level that produced it. Merging the two independently can yield {high,
// allow} out of a high→warn rule and a low→allow one, describing a mapping
// no rule configured.
//
// A producer wanting anything other than last-write-wins for its OWN keys
// folds them before writing: it can read the accumulated set off the
// EvalContext, which is cheaper than teaching this function every vocabulary
// in the process.
func mergeAnnotations(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string, len(src))
	}

	for k, v := range src {
		switch k {
		case AnnotationRiskLevel, AnnotationRiskAction:
			// Handled below, as a pair.
		default:
			dst[k] = v
		}
	}

	incoming, hasLevel := src[AnnotationRiskLevel]
	if !hasLevel {
		// An action with no level of its own: a producer that denied
		// without ever classifying. A denial short-circuits the chain,
		// so this is the last word on what happened to the statement
		// and it keeps any level an earlier rule established.
		if action, ok := src[AnnotationRiskAction]; ok {
			dst[AnnotationRiskAction] = action
		}
		return dst
	}
	if riskRank(incoming) <= riskRank(dst[AnnotationRiskLevel]) {
		// Strictly lower, or a tie the incumbent already answered.
		return dst
	}
	dst[AnnotationRiskLevel] = incoming
	if action, ok := src[AnnotationRiskAction]; ok {
		dst[AnnotationRiskAction] = action
	} else {
		// A level with no action would otherwise sit beside the
		// previous rule's action and misreport what was done.
		delete(dst, AnnotationRiskAction)
	}
	return dst
}

// Evaluate implements Evaluator.
func (c Chain) Evaluate(stmt inspect.Statement) Verdict {
	return c.EvaluateWith(stmt, &EvalContext{})
}

// EvaluateWith implements ContextualEvaluator, so a caller can seed the
// per-connection facts and so chains nest.
//
// Evaluators inside see what the ones before them established, which is how a
// producer's finding reaches a policy and how a policy reaches back to
// request a producer that would not otherwise have run.
func (c Chain) EvaluateWith(stmt inspect.Statement, ec *EvalContext) Verdict {
	var errs error
	for _, e := range c {
		var v Verdict
		if ce, ok := e.(ContextualEvaluator); ok {
			v = ce.EvaluateWith(stmt, ec)
		} else {
			v = e.Evaluate(stmt)
		}
		// Annotations survive an allow, which is the point of them: the
		// analyzer's risk level belongs in the audit record whether or
		// not anything denied.
		ec.Annotations = mergeAnnotations(ec.Annotations, v.Annotations)
		if v.Denied {
			v.Err = errors.Join(errs, v.Err)
			v.Annotations = ec.Annotations
			return v
		}
		errs = errors.Join(errs, v.Err)
	}
	return Verdict{Err: errs, Annotations: ec.Annotations}
}
